package relay

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

type policyAuthority struct {
	guard  sync.Mutex
	mu     sync.Mutex
	cap    int
	active bool
	seen   chan struct{}
}

func (a *policyAuthority) AuthenticateRelay(_ context.Context, token string) (auth.RelayCredential, error) {
	a.mu.Lock()
	active, cap := a.active, a.cap
	a.mu.Unlock()
	if a.seen != nil {
		select {
		case a.seen <- struct{}{}:
		default:
		}
	}
	if !active || token != "authorized" {
		return auth.RelayCredential{}, auth.ErrInvalidAccess
	}
	return auth.RelayCredential{AccountID: "a", SessionLimit: cap, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

func (a *policyAuthority) WithAccountGuards(_ context.Context, _ []string, work func()) bool {
	a.guard.Lock()
	defer a.guard.Unlock()
	work()
	return true
}

func (a *policyAuthority) change(active bool, cap int, s *Server) {
	a.WithAccountGuards(context.Background(), []string{"a"}, func() {
		a.mu.Lock()
		a.active, a.cap = active, cap
		a.mu.Unlock()
		if !active || s.AccountSessions("a") > cap {
			s.CloseAccount("a")
		}
	})
}

func TestLiveSessionCapReductionClosesAllAndReconnectsWithinCap(t *testing.T) {
	a := &policyAuthority{active: true, cap: 2}
	s, err := New(a, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	clients := make([]host.Host, 0, 2)
	for i := 0; i < 2; i++ {
		h, e := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
		if e != nil {
			t.Fatal(e)
		}
		clients = append(clients, h)
		defer h.Close()
		if e = h.Connect(ctx, ai); e != nil {
			t.Fatal(e)
		}
		if response := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
			t.Fatal(response)
		}
	}
	if got := s.AccountSessions("a"); got != 2 {
		t.Fatalf("initial sessions=%d", got)
	}
	start := time.Now()
	a.change(true, 1, s)
	if got := s.AccountSessions("a"); got != 0 || time.Since(start) > 10*time.Second {
		t.Fatalf("cap reduction left %d sessions after %s", got, time.Since(start))
	}
	for _, h := range clients {
		for len(h.Network().ConnsToPeer(s.Host.ID())) != 0 && time.Since(start) < time.Second {
			time.Sleep(10 * time.Millisecond)
		}
		if len(h.Network().ConnsToPeer(s.Host.ID())) != 0 {
			t.Fatal("old physical connection stayed open")
		}
	}
	if err := clients[0].Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	// A reconnect gets one slot; a second existing client gets a limit response.
	if response := authResponse(t, ctx, clients[0], s.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
		t.Fatal(response)
	}
	if err := clients[1].Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	if response := authResponse(t, ctx, clients[1], s.Host.ID(), "authorized"); !strings.Contains(response, "session_limit_exceeded") {
		t.Fatal(response)
	}
	a.change(false, 1, s)
	if got := s.AccountSessions("a"); got != 0 {
		t.Fatalf("revocation left %d sessions", got)
	}
}

func TestSecurityMutationFencesInFlightRelayAuthentication(t *testing.T) {
	a := &policyAuthority{active: true, cap: 1, seen: make(chan struct{}, 1)}
	s, err := New(a, wireCredit{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if err := h.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	conns := s.Host.Network().ConnsToPeer(h.ID())
	if len(conns) != 1 {
		t.Fatalf("physical connections=%d", len(conns))
	}
	a.guard.Lock()
	result := make(chan string, 1)
	go func() { _, _, code := s.authenticate(ctx, conns[0], "authorized"); result <- code }()
	select {
	case <-a.seen:
	case <-ctx.Done():
		a.guard.Unlock()
		t.Fatal("authentication did not reach the pre-guard lookup")
	}
	a.mu.Lock()
	a.active = false
	a.mu.Unlock()
	a.guard.Unlock()
	select {
	case code := <-result:
		if code != "authentication_failed" {
			t.Fatalf("stale admission result=%q", code)
		}
	case <-ctx.Done():
		t.Fatal("authentication remained blocked")
	}
	if got := s.ActiveSessions(); got != 0 {
		t.Fatalf("stale authority installed %d sessions", got)
	}
}
