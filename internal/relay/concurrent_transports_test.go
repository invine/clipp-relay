package relay

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/libp2p/go-libp2p/p2p/transport/websocket"
	ma "github.com/multiformats/go-multiaddr"
)

// Each host has the same Device Identity but a separate real physical transport,
// ensuring every request is sent over exactly the connection under test.
type transportClients struct {
	server    *Server
	ctx       context.Context
	key       crypto.PrivKey
	addresses map[string]ma.Multiaddr
	tlsConfig *tls.Config
}

func newTransportClients(t *testing.T, a Authority, cap int) *transportClients {
	t.Helper()
	s, err := New(a, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", WebSocketListenAddress: "/ip4/127.0.0.1/tcp/0/ws", WebRTCListenAddress: "/ip4/127.0.0.1/udp/0/webrtc-direct", MaxSessions: cap})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	addresses := map[string]ma.Multiaddr{}
	for _, family := range []string{"tcp", "websocket", "webrtc-direct"} {
		addresses[family], err = s.ListenAddressFor(family)
		if err != nil {
			t.Fatal(err)
		}
	}
	public, roots := localTLSProxy(t, addresses["websocket"], "relay.example.test")
	addresses["websocket"] = public
	return &transportClients{s, ctx, key, addresses, &tls.Config{RootCAs: roots}}
}

func (f *transportClients) connect(t *testing.T, family string, key crypto.PrivKey) host.Host {
	t.Helper()
	if key == nil {
		key = f.key
	}
	opts := []libp2p.Option{libp2p.Identity(key), libp2p.NoListenAddrs, libp2p.EnableRelay()}
	if family == "websocket" {
		opts = append(opts, libp2p.NoTransports, libp2p.Transport(websocket.New, websocket.WithTLSClientConfig(f.tlsConfig)))
	}
	h, err := libp2p.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if err := h.Connect(f.ctx, f.info(family)); err != nil {
		t.Fatal(err)
	}
	conns := h.Network().ConnsToPeer(f.server.Host.ID())
	marker := map[string]string{"tcp": "/tcp/", "websocket": "/wss", "webrtc-direct": "/webrtc-direct"}[family]
	if len(conns) != 1 || !strings.Contains(conns[0].RemoteMultiaddr().String(), marker) {
		t.Fatalf("unexpected %s connections: %v", family, conns)
	}
	return h
}
func (f *transportClients) info(family string) peer.AddrInfo {
	return peer.AddrInfo{ID: f.server.Host.ID(), Addrs: []ma.Multiaddr{f.addresses[family]}}
}
func (f *transportClients) auth(t *testing.T, h host.Host, token, expected string) {
	t.Helper()
	if response := authResponse(t, f.ctx, h, f.server.Host.ID(), token); !strings.Contains(response, expected) {
		t.Fatalf("authentication: %s, want %s", response, expected)
	}
}
func (f *transportClients) reserve(t *testing.T, h host.Host, family string) {
	t.Helper()
	if _, err := client.Reserve(f.ctx, h, f.info(family)); err != nil {
		t.Fatalf("reserve %s: %v", family, err)
	}
}

func TestConcurrentTransportSessionsAuthenticateIndependently(t *testing.T) {
	f := newTransportClients(t, lifecycleAuthority{}, 3)
	clients := map[string]host.Host{}
	for _, family := range []string{"tcp", "websocket", "webrtc-direct"} {
		h := f.connect(t, family, nil)
		clients[family] = h
		if _, err := client.Reserve(f.ctx, h, f.info(family)); err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
			t.Fatalf("%s inherited authentication: %v", family, err)
		}
		f.auth(t, h, "long", `"ok":true`)
	}
	if got := f.server.AccountSessions("a"); got != 3 {
		t.Fatalf("physical account sessions=%d, want 3", got)
	}
	for _, family := range []string{"tcp", "websocket", "webrtc-direct"} {
		f.reserve(t, clients[family], family)
	}
	_ = clients["tcp"].Close()
	until := time.Now().Add(time.Second)
	for f.server.ActiveSessions() != 2 && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.server.ActiveSessions() != 2 {
		t.Fatal("transport loss removed healthy sessions")
	}
	f.reserve(t, clients["websocket"], "websocket")
	f.reserve(t, clients["webrtc-direct"], "webrtc-direct")
}

func waitTransportSessions(t *testing.T, s *Server, count int) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for s.ActiveSessions() != count && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := s.ActiveSessions(); got != count {
		t.Fatalf("physical sessions=%d, want %d", got, count)
	}
}
func waitTransportClosed(t *testing.T, f *transportClients, h host.Host) {
	t.Helper()
	until := time.Now().Add(2 * time.Second)
	for len(h.Network().ConnsToPeer(f.server.Host.ID())) != 0 && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(h.Network().ConnsToPeer(f.server.Host.ID())) != 0 {
		t.Fatal("replaced or expired physical transport remains connected")
	}
}

type transportLimitAuthority struct{ limit int }

func (a transportLimitAuthority) AuthenticateRelay(ctx context.Context, token string) (auth.RelayCredential, error) {
	c, err := lifecycleAuthority{}.AuthenticateRelay(ctx, token)
	c.SessionLimit = a.limit
	return c, err
}

func TestConcurrentTransportPhysicalLimitsAndSameFamilyReplacement(t *testing.T) {
	for _, ceiling := range []string{"account", "global"} {
		t.Run(ceiling, func(t *testing.T) {
			accountCap, globalCap := 5, 2
			if ceiling == "account" {
				accountCap, globalCap = 2, 5
			}
			f := newTransportClients(t, transportLimitAuthority{accountCap}, globalCap)
			tcp := f.connect(t, "tcp", nil)
			ws := f.connect(t, "websocket", nil)
			f.auth(t, tcp, "long", `"ok":true`)
			f.auth(t, ws, "long", `"ok":true`)
			rtc := f.connect(t, "webrtc-direct", nil)
			f.auth(t, rtc, "long", "session_limit_exceeded")
			waitTransportClosed(t, f, rtc)
			waitTransportSessions(t, f.server, 2)
			f.reserve(t, tcp, "tcp")
			f.reserve(t, ws, "websocket")
			replacement := f.connect(t, "tcp", nil)
			f.auth(t, replacement, "invalid", "authentication_failed")
			waitTransportClosed(t, f, replacement)
			f.reserve(t, tcp, "tcp")
			replacement = f.connect(t, "tcp", nil)
			f.auth(t, replacement, "long", `"ok":true`)
			waitTransportClosed(t, f, tcp)
			waitTransportSessions(t, f.server, 2)
			f.reserve(t, replacement, "tcp")
			f.reserve(t, ws, "websocket")
			_ = replacement.Close()
			waitTransportSessions(t, f.server, 1)
			rtc = f.connect(t, "webrtc-direct", nil)
			f.auth(t, rtc, "long", `"ok":true`)
			waitTransportSessions(t, f.server, 2)
		})
	}
}

func TestConcurrentTransportCrossAccountReplacementAtGlobalCapacity(t *testing.T) {
	f := newTransportClients(t, lifecycleAuthority{}, 3)
	tcp := f.connect(t, "tcp", nil)
	ws := f.connect(t, "websocket", nil)
	f.auth(t, tcp, "long", `"ok":true`)
	f.auth(t, ws, "long", `"ok":true`)
	otherKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := f.connect(t, "tcp", otherKey)
	f.auth(t, unrelated, "other", `"ok":true`)
	next := f.connect(t, "webrtc-direct", nil)
	f.auth(t, next, "invalid", "authentication_failed")
	waitTransportClosed(t, f, next)
	waitTransportSessions(t, f.server, 3)
	f.reserve(t, tcp, "tcp")
	f.reserve(t, ws, "websocket")
	next = f.connect(t, "webrtc-direct", nil)
	f.auth(t, next, "other", `"ok":true`)
	waitTransportClosed(t, f, tcp)
	waitTransportClosed(t, f, ws)
	waitTransportSessions(t, f.server, 2)
	if f.server.AccountSessions("a") != 0 || f.server.AccountSessions("b") != 2 {
		t.Fatal("cross-account replacement mixed peer authority or removed an unrelated peer")
	}
	f.reserve(t, next, "webrtc-direct")
	f.reserve(t, unrelated, "tcp")
	f.server.CloseAccount("a")
	f.reserve(t, next, "webrtc-direct")
}

func TestConcurrentTransportRenewalExpiryAndAccountWideClosure(t *testing.T) {
	f := newTransportClients(t, lifecycleAuthority{}, 4)
	tcp := f.connect(t, "tcp", nil)
	ws := f.connect(t, "websocket", nil)
	rtc := f.connect(t, "webrtc-direct", nil)
	f.auth(t, tcp, "short", `"ok":true`)
	f.auth(t, ws, "short", `"ok":true`)
	f.auth(t, rtc, "long", `"ok":true`)
	f.auth(t, tcp, "invalid", "authentication_failed")
	f.auth(t, ws, "long", `"ok":true`)
	waitTransportClosed(t, f, tcp)
	waitTransportSessions(t, f.server, 2)
	f.reserve(t, ws, "websocket")
	f.reserve(t, rtc, "webrtc-direct")
	f.auth(t, ws, "other", "account_change_requires_new_connection")
	otherKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := f.connect(t, "tcp", otherKey)
	f.auth(t, unrelated, "other", `"ok":true`)
	f.server.CloseAccount("a")
	waitTransportClosed(t, f, ws)
	waitTransportClosed(t, f, rtc)
	waitTransportSessions(t, f.server, 1)
	f.reserve(t, unrelated, "tcp")
}

func TestConcurrentTransportReservationAndLeaseSurviveUnrelatedTransportLoss(t *testing.T) {
	f := newTransportClients(t, lifecycleAuthority{}, 4)
	tcp := f.connect(t, "tcp", nil)
	ws := f.connect(t, "websocket", nil)
	rtc := f.connect(t, "webrtc-direct", nil)
	for _, h := range []host.Host{tcp, ws, rtc} {
		f.auth(t, h, "long", `"ok":true`)
	}
	register := func(h host.Host, family string) {
		t.Helper()
		f.reserve(t, h, family)
		request, _ := json.Marshal(map[string]any{"action": "register", "topic": "clipp", "signedPeerRecord": base64.RawURLEncoding.EncodeToString(rvEnvelope(t, h, f.server.Host.ID()))})
		if got := rvExchange(t, f.ctx, h, f.server.Host.ID(), string(RendezvousV2Protocol), request); string(got["ok"]) != "true" {
			t.Fatalf("register %s: %v", family, got)
		}
	}
	register(tcp, "tcp")
	register(ws, "websocket")
	lookup, _ := json.Marshal(map[string]any{"action": "lookup", "topic": "clipp", "peerId": ws.ID().String()})
	// Closing an old reservation/lease owner cannot remove the newer WSS owner.
	_ = tcp.Close()
	waitTransportSessions(t, f.server, 2)
	if got := rvExchange(t, f.ctx, rtc, f.server.Host.ID(), string(RendezvousV2Protocol), lookup); got["record"] == nil {
		t.Fatalf("healthy owner's lease removed by old close: %v", got)
	}
	originKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	origin := f.connect(t, "tcp", originKey)
	f.auth(t, origin, "other", `"ok":true`)
	transfer := func(destination host.Host) {
		t.Helper()
		destination.SetStreamHandler("/clipp/concurrent-test/1.0.0", func(st network.Stream) {
			defer st.Close()
			_, _ = io.Copy(st, st)
		})
		address := ma.StringCast("/p2p/" + f.server.Host.ID().String() + "/p2p-circuit/p2p/" + destination.ID().String())
		if err := origin.Connect(f.ctx, peer.AddrInfo{ID: destination.ID(), Addrs: []ma.Multiaddr{address}}); err != nil {
			t.Fatal(err)
		}
		st, err := origin.NewStream(network.WithAllowLimitedConn(f.ctx, "test"), destination.ID(), "/clipp/concurrent-test/1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		_ = st.SetDeadline(time.Now().Add(3 * time.Second))
		if !strings.Contains(st.Conn().RemoteMultiaddr().String(), "/p2p-circuit") {
			t.Fatal("traffic bypassed relay")
		}
		if _, err := st.Write([]byte("relay survives")); err != nil {
			t.Fatal(err)
		}
		reply := make([]byte, len("relay survives"))
		if _, err := io.ReadFull(st, reply); err != nil || string(reply) != "relay survives" {
			t.Fatalf("opaque roundtrip %q: %v", reply, err)
		}
		_ = st.Close()
		_ = origin.Network().ClosePeer(destination.ID())
	}
	transfer(ws)
	// Stock keeps its single per-peer reservation while WebRTC stays connected;
	// owner loss removes that owner's lease, and STOP can still use healthy auth.
	_ = ws.Close()
	waitTransportSessions(t, f.server, 2)
	if got := rvExchange(t, f.ctx, rtc, f.server.Host.ID(), string(RendezvousV2Protocol), lookup); got["record"] != nil {
		t.Fatalf("lost owner's lease remains: %v", got)
	}
	transfer(rtc)
	register(rtc, "webrtc-direct")
	if got := rvExchange(t, f.ctx, origin, f.server.Host.ID(), string(RendezvousV1Protocol), lookup); got["record"] == nil {
		t.Fatalf("RVv1 could not find repaired lease: %v", got)
	}
}

func TestConcurrentTransportAdmissionsCannotOverrunPhysicalSessionCap(t *testing.T) {
	f := newTransportClients(t, transportLimitAuthority{2}, 2)
	clients := []host.Host{f.connect(t, "tcp", nil), f.connect(t, "websocket", nil), f.connect(t, "webrtc-direct", nil)}
	start := make(chan struct{})
	results := make(chan string, len(clients))
	var workers sync.WaitGroup
	for _, h := range clients {
		workers.Add(1)
		go func(h host.Host) {
			defer workers.Done()
			<-start
			response, err := transportAuthResponse(f.ctx, h, f.server.Host.ID(), "long")
			if err != nil {
				results <- err.Error()
				return
			}
			results <- response
		}(h)
	}
	close(start)
	workers.Wait()
	close(results)
	success, refused := 0, 0
	for result := range results {
		switch {
		case strings.Contains(result, `"ok":true`):
			success++
		case strings.Contains(result, "session_limit_exceeded"):
			refused++
		default:
			t.Fatalf("unexpected racing admission: %s", result)
		}
	}
	if success != 2 || refused != 1 || f.server.AccountSessions("a") != 2 {
		t.Fatalf("race admitted=%d refused=%d physical=%d", success, refused, f.server.ActiveSessions())
	}
}

func transportAuthResponse(ctx context.Context, h host.Host, id peer.ID, token string) (string, error) {
	st, err := h.NewStream(ctx, id, AuthProtocol)
	if err != nil {
		return "", err
	}
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = st.Write(frame(fmt.Sprintf(`{"accessToken":%q}`, token))); err != nil {
		return "", err
	}
	if err = st.CloseWrite(); err != nil {
		return "", err
	}
	size, err := readSize(st)
	if err != nil {
		return "", err
	}
	data := make([]byte, size)
	_, err = io.ReadFull(st, data)
	return string(data), err
}
