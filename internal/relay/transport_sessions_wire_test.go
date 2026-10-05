package relay

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
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

func TestCrossTransportSamePeerReplacementAtCapacity(t *testing.T) {
	for _, ceiling := range []string{"account", "global"} {
		t.Run(ceiling, func(t *testing.T) {
			accountCap, globalCap := 5, 1
			if ceiling == "account" {
				accountCap, globalCap = 1, 5
			}
			f := newTransportClients(t, transportLimitAuthority{accountCap}, globalCap)
			current := f.connect(t, "tcp", nil)
			f.auth(t, current, "long", `"ok":true`)
			f.reserve(t, current, "tcp")
			currentFamily := "tcp"
			for _, family := range []string{"websocket", "webrtc-direct", "tcp"} {
				rejected := f.connect(t, family, nil)
				f.auth(t, rejected, "invalid", "authentication_failed")
				waitTransportClosed(t, f, rejected)
				if f.server.AccountSessions("a") != 1 || len(current.Network().ConnsToPeer(f.server.Host.ID())) != 1 {
					t.Fatal("failed admission changed the authoritative session")
				}
				f.reserve(t, current, currentFamily)
				otherKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				distinct := f.connect(t, family, otherKey)
				f.auth(t, distinct, "long", "session_limit_exceeded")
				waitTransportClosed(t, f, distinct)
				next := f.connect(t, family, nil)
				if _, err := client.Reserve(f.ctx, next, f.info(family)); err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
					t.Fatalf("%s inherited authentication: %v", family, err)
				}
				f.auth(t, next, "long", `"ok":true`)
				waitTransportClosed(t, f, current)
				waitTransportSessions(t, f.server, 1)
				if got := f.server.AccountSessions("a"); got != 1 {
					t.Fatalf("%s replacement account sessions=%d, want 1", family, got)
				}
				f.reserve(t, next, family)
				current = next
				currentFamily = family
			}
		})
	}
}

func TestCrossTransportCrossAccountReplacementAtGlobalCapacity(t *testing.T) {
	f := newTransportClients(t, lifecycleAuthority{}, 2)
	current := f.connect(t, "tcp", nil)
	f.auth(t, current, "long", `"ok":true`)
	otherKey, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := f.connect(t, "websocket", otherKey)
	f.auth(t, unrelated, "other", `"ok":true`)
	rejected := f.connect(t, "webrtc-direct", nil)
	f.auth(t, rejected, "invalid", "authentication_failed")
	waitTransportClosed(t, f, rejected)
	waitTransportSessions(t, f.server, 2)
	f.reserve(t, current, "tcp")
	f.reserve(t, unrelated, "websocket")
	next := f.connect(t, "webrtc-direct", nil)
	f.auth(t, next, "other", `"ok":true`)
	waitTransportClosed(t, f, current)
	waitTransportSessions(t, f.server, 2)
	if f.server.AccountSessions("a") != 0 || f.server.AccountSessions("b") != 2 {
		t.Fatal("cross-account replacement mixed peer authority or removed an unrelated peer")
	}
	f.reserve(t, next, "webrtc-direct")
	f.reserve(t, unrelated, "websocket")
	f.server.CloseAccount("a")
	f.reserve(t, next, "webrtc-direct")
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
