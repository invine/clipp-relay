package relay

import (
	"context"
	"crypto/rand"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	libp2pwebrtc "github.com/libp2p/go-libp2p/p2p/transport/webrtc"
	ma "github.com/multiformats/go-multiaddr"
)

func TestTransportOnlyListenersRequireRelayAuth(t *testing.T) {
	for _, tc := range []struct {
		name        string
		opts        Options
		marker      string
		boundMarker string
	}{
		{"websocket", Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws"}, "/ws", "/ws"},
		{"webrtc-direct", Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"}, "/webrtc-direct", "/webrtc-direct/certhash/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(wireAuthority{}, wireCredit{}, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			addr, err := s.ListenAddressFor(tc.name)
			if err != nil || !strings.Contains(addr.String(), tc.boundMarker) {
				t.Fatalf("address = %v, %v", addr, err)
			}
			h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: []ma.Multiaddr{addr}}
			if err := h.Connect(ctx, ai); err != nil {
				t.Fatal(err)
			}
			for _, c := range h.Network().ConnsToPeer(s.Host.ID()) {
				if !strings.Contains(c.RemoteMultiaddr().String(), tc.marker) {
					t.Fatalf("unexpected transport %s", c.RemoteMultiaddr())
				}
			}
			if _, err := client.Reserve(ctx, h, ai); err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
				t.Fatalf("unauthenticated reserve: %v", err)
			}
			sendWireAuth(t, ctx, h, s.Host.ID())
			if _, err := client.Reserve(ctx, h, ai); err != nil {
				t.Fatalf("authenticated reserve: %v", err)
			}
		})
	}
}

type lifecycleAuthority struct{}

func (lifecycleAuthority) AuthenticateRelay(_ context.Context, token string) (auth.RelayCredential, error) {
	account := "a"
	lifetime := 5 * time.Minute
	switch token {
	case "short":
		lifetime = 2 * time.Second
	case "expire":
		lifetime = 300 * time.Millisecond
	case "long":
	case "other":
		account = "b"
	default:
		return auth.RelayCredential{}, auth.ErrInvalidAccess
	}
	return auth.RelayCredential{AccountID: account, SessionLimit: 5, ExpiresAt: time.Now().Add(lifetime)}, nil
}

func TestTransportOnlyRenewalExpiryAndReplacement(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"websocket", Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws"}},
		{"webrtc-direct", Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(lifecycleAuthority{}, wireCredit{}, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			key, _, err := crypto.GenerateEd25519Key(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			first, err := libp2p.New(libp2p.Identity(key), libp2p.NoListenAddrs, libp2p.DisableRelay())
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			second, err := libp2p.New(libp2p.Identity(key), libp2p.NoListenAddrs, libp2p.DisableRelay())
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			addr, err := s.ListenAddressFor(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: []ma.Multiaddr{addr}}
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			if err := first.Connect(ctx, ai); err != nil {
				t.Fatal(err)
			}
			if resp := authResponse(t, ctx, first, s.Host.ID(), "short"); !strings.Contains(resp, `"ok":true`) {
				t.Fatal(resp)
			}
			if resp := authResponse(t, ctx, first, s.Host.ID(), "invalid"); !strings.Contains(resp, "authentication_failed") {
				t.Fatal(resp)
			}
			time.Sleep(1050 * time.Millisecond)
			if resp := authResponse(t, ctx, first, s.Host.ID(), "long"); !strings.Contains(resp, `"ok":true`) {
				t.Fatal(resp)
			}
			time.Sleep(1100 * time.Millisecond)
			if _, err := client.Reserve(ctx, first, ai); err != nil {
				t.Fatalf("renewed session expired at old deadline: %v", err)
			}
			if resp := authResponse(t, ctx, first, s.Host.ID(), "other"); !strings.Contains(resp, "account_change_requires_new_connection") {
				t.Fatal(resp)
			}
			if s.AccountSessions("a") != 1 || s.AccountSessions("b") != 0 {
				t.Fatal("same-connection account change altered ownership")
			}
			if err := second.Connect(ctx, ai); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Reserve(ctx, second, ai); err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
				t.Fatalf("replacement inherited auth before token: %v", err)
			}
			if resp := authResponse(t, ctx, second, s.Host.ID(), "long"); !strings.Contains(resp, `"ok":true`) {
				t.Fatal(resp)
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) && len(first.Network().ConnsToPeer(s.Host.ID())) != 0 {
				time.Sleep(10 * time.Millisecond)
			}
			if len(first.Network().ConnsToPeer(s.Host.ID())) != 0 || s.ActiveSessions() != 1 {
				t.Fatal("replacement retained old physical connection or duplicate session")
			}
			if _, err := client.Reserve(ctx, second, ai); err != nil {
				t.Fatalf("replacement reserve: %v", err)
			}
			if resp := authResponse(t, ctx, second, s.Host.ID(), "expire"); !strings.Contains(resp, `"ok":true`) {
				t.Fatal(resp)
			}
			deadline = time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) && len(second.Network().ConnsToPeer(s.Host.ID())) != 0 {
				time.Sleep(10 * time.Millisecond)
			}
			if len(second.Network().ConnsToPeer(s.Host.ID())) != 0 || s.ActiveSessions() != 0 {
				t.Fatal("expired session retained physical connection")
			}
		})
	}
}

func TestTransportOnlyCircuitsTransferAndChargeBothDirections(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"websocket", Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws"}},
		{"webrtc-direct", Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credit := runOpaqueCircuit(t, twoAuthority{}, tc.opts, tc.name)
			if credit.count("a") < int64(len("opaque relayed bytes")+len("return bytes")) || credit.count("b") < int64(len("opaque relayed bytes")+len("return bytes")) {
				t.Fatalf("missing endpoint charges: a=%d b=%d", credit.count("a"), credit.count("b"))
			}
		})
	}
}

func TestTransportOnlySameAccountChargesBothEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"websocket", Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws"}},
		{"webrtc-direct", Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credit := runOpaqueCircuit(t, sameAccountAuthority{}, tc.opts, tc.name)
			if got := credit.count("a"); got < int64(2*(len("opaque relayed bytes")+len("return bytes"))) {
				t.Fatalf("same-account charged %d bytes", got)
			}
			if got := credit.count("b"); got != 0 {
				t.Fatalf("other account charged %d bytes", got)
			}
		})
	}
}

func TestTransportMemoryPressureRejectsAndRecovers(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"websocket", Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws"}},
		{"webrtc-direct", Options{WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(wireAuthority{}, wireCredit{}, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			addr, err := s.ListenAddressFor(tc.name)
			if err != nil {
				t.Fatal(err)
			}
			h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			var reserved int
			if err := s.manager.ViewSystem(func(scope network.ResourceScope) error {
				reserved = int(512*mib - scope.Stat().Memory)
				return scope.ReserveMemory(reserved, network.ReservationPriorityAlways)
			}); err != nil {
				t.Fatal(err)
			}
			defer s.manager.ViewSystem(func(scope network.ResourceScope) error { scope.ReleaseMemory(reserved); return nil })
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: []ma.Multiaddr{addr}}
			err = h.Connect(ctx, ai)
			if err == nil {
				st, streamErr := h.NewStream(ctx, s.Host.ID(), AuthProtocol)
				if streamErr == nil {
					_ = st.SetDeadline(time.Now().Add(time.Second))
					_, streamErr = st.Write(frame(`{"accessToken":"authorized"}`))
					if streamErr == nil {
						streamErr = st.CloseWrite()
					}
					if streamErr == nil {
						_, streamErr = io.ReadAll(st)
					}
					_ = st.Close()
				}
				if streamErr == nil {
					t.Fatal("relay authentication succeeded under exhausted system RM memory")
				}
			}
			if s.ActiveSessions() != 0 {
				t.Fatal("relay admitted a session under exhausted system RM memory")
			}
			if err := s.manager.ViewSystem(func(scope network.ResourceScope) error { scope.ReleaseMemory(reserved); return nil }); err != nil {
				t.Fatal(err)
			}
			reserved = 0
			fresh, err := libp2p.New(libp2p.NoListenAddrs, libp2p.DisableRelay())
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			recoverCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
			defer stop()
			if err := fresh.Connect(recoverCtx, ai); err != nil {
				t.Fatalf("transport did not recover after pressure: %v", err)
			}
			if resp := authResponse(t, recoverCtx, fresh, s.Host.ID(), "authorized"); !strings.Contains(resp, `"ok":true`) {
				t.Fatalf("authentication did not recover after pressure: %s", resp)
			}
		})
	}
}

func TestWebSocketNegotiationTimeoutAndStockWebRTCInFlightBound(t *testing.T) {
	if libp2pwebrtc.DefaultMaxInFlightConnections != 128 {
		t.Fatalf("stock WebRTC pending setup bound = %d", libp2pwebrtc.DefaultMaxInFlightConnections)
	}
	s, err := New(wireAuthority{}, wireCredit{}, Options{WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.ListenAddressFor("websocket")
	if err != nil {
		t.Fatal(err)
	}
	port, err := a.ValueForProtocol(ma.P_TCP)
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	started := time.Now()
	_ = c.SetReadDeadline(started.Add(12 * time.Second))
	var one [1]byte
	_, err = c.Read(one[:])
	if err == nil {
		t.Fatal("idle unupgraded WebSocket connection remained open")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatalf("WebSocket server did not end negotiation within 12s: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 11*time.Second {
		t.Fatalf("WebSocket negotiation elapsed %s, expected stock 10s bound", elapsed)
	}
}
