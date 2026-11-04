package relay

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/quota"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

type validationOnlyAuthority struct{ calls atomic.Int64 }

func (a *validationOnlyAuthority) AuthenticateRelay(context.Context, string) (auth.RelayCredential, error) {
	a.calls.Add(1)
	return auth.RelayCredential{AccountID: "relay-account", GrantID: "grant", SessionLimit: 2, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

func TestRelayAuthenticationNeedsOnlyCredentialValidation(t *testing.T) {
	authority := &validationOnlyAuthority{}
	s, err := New(authority, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err := h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	response := authResponse(t, ctx, h, s.Host.ID(), "authorized")
	if !strings.Contains(response, `"ok":true`) {
		t.Fatalf("relay authentication failed: %s", response)
	}
	if authority.calls.Load() != 1 || s.AccountSessions("relay-account") != 1 {
		t.Fatalf("validation calls=%d installed sessions=%d", authority.calls.Load(), s.AccountSessions("relay-account"))
	}
}

type admissionWireAccounting struct {
	wireAccounting
	mu      sync.Mutex
	counts  map[string]int
	limit   int
	updates chan map[string]int
}

func (a *admissionWireAccounting) Assess(context.Context, string) (quota.Assessment, error) {
	return quota.Assessment{}, nil
}

func (a *admissionWireAccounting) CommitAdmission(_ quota.Assessment, change quota.SessionChange, install func() map[string]int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.counts["relay-account"]
	if change.Kind == quota.SessionNew || change.Kind == quota.SessionReplacement && change.PreviousAccount != "relay-account" {
		if current+1 > a.limit {
			return quota.ErrSessionLimit
		}
	}
	counts := install()
	for account, count := range counts {
		a.counts[account] = count
	}
	a.notifyLocked(counts)
	return nil
}

func (a *admissionWireAccounting) PublishLiveCounts(counts map[string]int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for account, count := range counts {
		a.counts[account] = count
	}
	a.notifyLocked(counts)
}

func (a *admissionWireAccounting) notifyLocked(counts map[string]int) {
	if a.updates == nil {
		return
	}
	update := make(map[string]int, len(counts))
	for account, count := range counts {
		update[account] = count
	}
	select {
	case a.updates <- update:
	default:
	}
}

func TestRelayCommitsFinalSessionLimitThroughAdmissionOwner(t *testing.T) {
	authority := &validationOnlyAuthority{}
	accounting := &admissionWireAccounting{counts: make(map[string]int), limit: 1, updates: make(chan map[string]int, 16)}
	s, err := New(authority, accounting, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	clients := make([]peerHost, 2)
	for i := range clients {
		h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close()
		clients[i] = peerHost{host: h}
		if err := h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
			t.Fatal(err)
		}
	}
	if response := authResponse(t, ctx, clients[0].host, s.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
		t.Fatalf("first session refused: %s", response)
	}
	if response := authResponse(t, ctx, clients[1].host, s.Host.ID(), "authorized"); !strings.Contains(response, `"code":"session_limit_exceeded"`) {
		t.Fatalf("second session response=%s", response)
	}
	if got := s.AccountSessions("relay-account"); got != 1 {
		t.Fatalf("Relay Session count=%d, want 1", got)
	}
	if len(clients[0].host.Network().ConnsToPeer(s.Host.ID())) != 1 {
		t.Fatal("refused admission displaced the existing Relay Session")
	}
	waitAdmissionCount(t, accounting.updates, "relay-account", 1)

	key := clients[0].host.Peerstore().PrivKey(clients[0].host.ID())
	replacement, err := libp2p.New(libp2p.Identity(key), libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if response := authResponse(t, ctx, replacement, s.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
		t.Fatalf("same-account replacement refused: %s", response)
	}
	if got := s.AccountSessions("relay-account"); got != 1 {
		t.Fatalf("same-account replacement count=%d, want 1", got)
	}
	waitAdmissionCount(t, accounting.updates, "relay-account", 1)
	if err := s.RetireGrant("grant"); err != nil {
		t.Fatal(err)
	}
	waitAdmissionCount(t, accounting.updates, "relay-account", 0)
	if got := s.AccountSessions("relay-account"); got != 0 {
		t.Fatalf("grant revocation left %d Relay Sessions", got)
	}
	if len(replacement.Network().ConnsToPeer(s.Host.ID())) != 1 {
		t.Fatal("grant revocation closed the physical connection before completion")
	}
	if err := s.CompleteGrantInvalidation("grant"); err != nil {
		t.Fatal(err)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
	if got, known := countFromAccounting(accounting, "relay-account"); !known || got != 0 {
		t.Fatalf("revoked Quota count=(%d,%v), want known zero", got, known)
	}
}

func waitAdmissionCount(t *testing.T, updates <-chan map[string]int, account string, want int) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case counts := <-updates:
			if got, ok := counts[account]; ok && got == want {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for Quota count %s=%d", account, want)
		}
	}
}

func countFromAccounting(a *admissionWireAccounting, account string) (int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	count, ok := a.counts[account]
	return count, ok
}

type peerHost struct{ host host.Host }
