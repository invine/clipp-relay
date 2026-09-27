package relay

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/quota"
	"crypto/rand"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	ma "github.com/multiformats/go-multiaddr"
)

type wireAuthority struct{}

func (wireAuthority) AuthenticateRelay(_ context.Context, token string) (auth.RelayCredential, error) {
	if token != "authorized" {
		return auth.RelayCredential{}, auth.ErrInvalidAccess
	}
	return auth.RelayCredential{AccountID: "a", SessionLimit: 5, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

type wireCredit struct{}

func (wireCredit) Ensure(context.Context, string, int64) (quota.Result, error) {
	return quota.Result{Committed: 65536, Usable: 65536}, nil
}
func (wireCredit) Take(context.Context, string, int64, int64) (quota.Result, error) {
	return quota.Result{Committed: 65536, Usable: 65536}, nil
}

func sendWireAuth(t *testing.T, ctx context.Context, h host.Host, id peer.ID) {
	sendWireToken(t, ctx, h, id, "authorized")
}
func sendWireToken(t *testing.T, ctx context.Context, h host.Host, id peer.ID, token string) {
	t.Helper()
	st, err := h.NewStream(ctx, id, AuthProtocol)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Write(frame(fmt.Sprintf(`{"accessToken":%q}`, token))); err != nil {
		t.Fatal(err)
	}
	if err = st.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	n, err := binary.ReadUvarint(byteReader{st})
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, n)
	if _, err = io.ReadFull(st, data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"ok":true`) {
		t.Fatalf("auth failed: %s", data)
	}
	_ = st.Close()
}

type twoAuthority struct{}

func (twoAuthority) AuthenticateRelay(_ context.Context, token string) (auth.RelayCredential, error) {
	if token != "a" && token != "b" {
		return auth.RelayCredential{}, auth.ErrInvalidAccess
	}
	return auth.RelayCredential{AccountID: token, SessionLimit: 5, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

type recordingCredit struct {
	mu     sync.Mutex
	counts map[string]int64
}

func (c *recordingCredit) Ensure(context.Context, string, int64) (quota.Result, error) {
	return quota.Result{Committed: 65536, Usable: 65536}, nil
}
func (c *recordingCredit) Take(_ context.Context, id string, _ int64, n int64) (quota.Result, error) {
	c.mu.Lock()
	c.counts[id] += n
	c.mu.Unlock()
	return quota.Result{Committed: 65536, Usable: 65536}, nil
}
func (c *recordingCredit) count(id string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[id]
}

func TestTCPAuthGatesStockHOP(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if err = h.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Reserve(ctx, h, ai); err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
		t.Fatalf("unauthenticated reserve: %v", err)
	}
	// The same Noise-authenticated connection becomes eligible after relay auth.
	sendWireAuth(t, ctx, h, s.Host.ID())
	if _, err = client.Reserve(ctx, h, ai); err != nil {
		t.Fatalf("stock reserve after auth: %v", err)
	}
}

func TestFullCapacitySamePeerReplacementIsOneForOne(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0", MaxSessions: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	first, err := libp2p.New(libp2p.Identity(key), libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := libp2p.New(libp2p.Identity(key), libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if err = first.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	sendWireAuth(t, ctx, first, s.Host.ID())
	if got := s.ActiveSessions(); got != 1 {
		t.Fatalf("first count %d", got)
	}
	if err = second.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Reserve(ctx, second, ai); err == nil || !strings.Contains(err.Error(), "PERMISSION_DENIED") {
		t.Fatalf("unauthenticated second physical connection inherited auth: %v", err)
	}
	sendWireAuth(t, ctx, second, s.Host.ID())
	if got := s.ActiveSessions(); got != 1 {
		t.Fatalf("replacement count %d", got)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(first.Network().ConnsToPeer(s.Host.ID())) != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(first.Network().ConnsToPeer(s.Host.ID())); n != 0 {
		t.Fatalf("old physical connection remains: %d", n)
	}
	if _, err = client.Reserve(ctx, second, ai); err != nil {
		t.Fatalf("replacement denied: %v", err)
	}
}

func TestSTOPPinsAuthenticatedPhysicalConnection(t *testing.T) {
	s, err := New(wireAuthority{}, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := libp2p.New(libp2p.Identity(key), libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	parallel, err := libp2p.New(libp2p.Identity(key), libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer parallel.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if err = destination.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	sendWireAuth(t, ctx, destination, s.Host.ID())
	if _, err = client.Reserve(ctx, destination, ai); err != nil {
		t.Fatal(err)
	}
	owned := s.authoritativeConn(destination.ID())
	if owned == nil {
		t.Fatal("missing authenticated connection")
	}
	if err = parallel.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	if n := len(s.Host.Network().ConnsToPeer(destination.ID())); n != 2 {
		t.Fatalf("want two physical connections, got %d", n)
	}
	adapter := gatedHost{Host: s.Host, stopConn: s.authoritativeConn}
	st, err := adapter.NewStream(ctx, destination.ID(), "/libp2p/circuit/relay/0.2.0/stop")
	if err != nil {
		t.Fatal(err)
	}
	if st.Conn() != owned {
		t.Fatal("STOP selected unauthenticated parallel connection")
	}
	_ = st.Reset()
}

type sameAccountAuthority struct{}

func (sameAccountAuthority) AuthenticateRelay(_ context.Context, token string) (auth.RelayCredential, error) {
	if token != "a" && token != "b" {
		return auth.RelayCredential{}, auth.ErrInvalidAccess
	}
	return auth.RelayCredential{AccountID: "a", SessionLimit: 5, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

func runOpaqueCircuit(t *testing.T, authority Authority) *recordingCredit {
	t.Helper()
	credit := &recordingCredit{counts: map[string]int64{}}
	s, err := New(authority, credit, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	for _, h := range []host.Host{a, b} {
		if err = h.Connect(ctx, ai); err != nil {
			t.Fatal(err)
		}
	}
	sendWireToken(t, ctx, a, s.Host.ID(), "a")
	sendWireToken(t, ctx, b, s.Host.ID(), "b")
	if _, err = client.Reserve(ctx, a, ai); err != nil {
		t.Fatal(err)
	}
	received := make(chan []byte, 1)
	a.SetStreamHandler("/clipp/test/1.0.0", func(st network.Stream) {
		defer st.Close()
		data := make([]byte, 1024)
		n, e := st.Read(data)
		if e == nil {
			received <- data[:n]
		}
	})
	raddr, err := ma.NewMultiaddr(fmt.Sprintf("/p2p/%s/p2p-circuit/p2p/%s", s.Host.ID(), a.ID()))
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Connect(ctx, peer.AddrInfo{ID: a.ID(), Addrs: []ma.Multiaddr{raddr}}); err != nil {
		t.Fatal(err)
	}
	st, err := b.NewStream(network.WithAllowLimitedConn(ctx, "test"), a.ID(), "/clipp/test/1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("opaque relayed bytes")
	if _, err = st.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = st.CloseWrite()
	select {
	case got := <-received:
		if string(got) != string(payload) {
			t.Fatalf("payload %q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_ = st.Close()
	return credit
}

func TestCrossAccountCircuitForwardsOpaqueBytesAndChargesBothEndpoints(t *testing.T) {
	credit := runOpaqueCircuit(t, twoAuthority{})
	if credit.count("a") == 0 || credit.count("b") == 0 {
		t.Fatalf("endpoint charges a=%d b=%d", credit.count("a"), credit.count("b"))
	}
}

func TestSameAccountCircuitChargesBothEndpoints(t *testing.T) {
	credit := runOpaqueCircuit(t, sameAccountAuthority{})
	if got := credit.count("a"); got < int64(2*len("opaque relayed bytes")) {
		t.Fatalf("same-account endpoint charge = %d", got)
	}
	if got := credit.count("b"); got != 0 {
		t.Fatalf("charged other account %d", got)
	}
}
