package relay

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/quota"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
)

type failingAuthority struct{ err error }

type countingAuthority struct{ calls atomic.Int64 }

func (a *countingAuthority) AuthenticateRelay(context.Context, string) (auth.RelayCredential, error) {
	a.calls.Add(1)
	return auth.RelayCredential{AccountID: "a", SessionLimit: 2, ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func (a failingAuthority) AuthenticateRelay(context.Context, string) (auth.RelayCredential, error) {
	return auth.RelayCredential{}, a.err
}

type blockingEligibility struct {
	wireAccounting
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	armed   bool
}

type changingCapEligibility struct {
	wireAccounting
	mu      sync.Mutex
	cap     int
	entered chan struct{}
	release chan struct{}
	blocked bool
}

func (c *changingCapEligibility) Eligibility(ctx context.Context, _ string) (quota.AccountingEligibility, error) {
	c.mu.Lock()
	block := c.blocked
	c.blocked = false
	cap := c.cap
	c.mu.Unlock()
	if block {
		close(c.entered)
		select {
		case <-c.release:
		case <-ctx.Done():
			return quota.AccountingEligibility{}, ctx.Err()
		}
	}
	return quota.AccountingEligibility{SessionLimit: cap}, nil
}

func (c *changingCapEligibility) arm(cap int) {
	c.mu.Lock()
	c.cap = cap
	c.blocked = true
	c.mu.Unlock()
}

type oldCapAuthority struct{}

func (oldCapAuthority) AuthenticateRelay(context.Context, string) (auth.RelayCredential, error) {
	return auth.RelayCredential{AccountID: "a", SessionLimit: 5, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

func TestCurrentQuotaSessionLimitFencesPendingAuthentication(t *testing.T) {
	accounting := &changingCapEligibility{cap: 5, entered: make(chan struct{}), release: make(chan struct{})}
	s, err := New(oldCapAuthority{}, accounting, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	clients := make([]host.Host, 4)
	for i := range clients {
		clients[i], err = libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
		if err != nil {
			t.Fatal(err)
		}
		defer clients[i].Close()
		if err = clients[i].Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if response := authResponse(t, ctx, clients[i], s.Host.ID(), "old-policy"); !strings.Contains(response, `"ok":true`) {
			t.Fatalf("session %d: %s", i, response)
		}
	}
	if got := s.AccountSessions("a"); got != 3 {
		t.Fatalf("live sessions=%d, want 3", got)
	}
	accounting.arm(3)
	result := make(chan string, 1)
	go func() {
		response, _ := transportAuthResponse(ctx, clients[3], s.Host.ID(), "old-policy")
		result <- response
	}()
	select {
	case <-accounting.entered:
	case <-ctx.Done():
		t.Fatal("fourth authentication did not reach Quota")
	}
	close(accounting.release)
	select {
	case response := <-result:
		if !strings.Contains(response, `"code":"session_limit_exceeded"`) {
			t.Fatalf("current cap response: %s", response)
		}
	case <-ctx.Done():
		t.Fatal("fourth authentication did not finish")
	}
	if got := s.AccountSessions("a"); got != 3 {
		t.Fatalf("live sessions after cap reduction=%d, want 3", got)
	}
}

func (c *blockingEligibility) arm() {
	c.mu.Lock()
	c.armed = true
	c.mu.Unlock()
}

func (c *blockingEligibility) Eligibility(context.Context, string) (quota.AccountingEligibility, error) {
	c.mu.Lock()
	armed := c.armed
	if armed {
		c.armed = false
	}
	c.mu.Unlock()
	if armed {
		c.once.Do(func() {
			close(c.entered)
			<-c.release
		})
	}
	return quota.AccountingEligibility{SessionLimit: 5}, nil
}

type failingAccounting struct{ err error }

func (c failingAccounting) Eligibility(context.Context, string) (quota.AccountingEligibility, error) {
	return quota.AccountingEligibility{}, c.err
}

func authResponse(t *testing.T, ctx context.Context, h host.Host, id peer.ID, token string) string {
	t.Helper()
	response, err := transportAuthResponse(ctx, h, id, token)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func readSize(r io.Reader) (uint64, error) { return binary.ReadUvarint(byteReader{r}) }

func TestDrainRejectsAuthBeforeAuthorityCall(t *testing.T) {
	a := &countingAuthority{}
	s, err := New(a, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if response := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
		t.Fatal(response)
	}
	before := a.calls.Load()
	s.StartDrain()
	if response := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(response, `"code":"temporarily_unavailable"`) {
		t.Fatal(response)
	}
	if a.calls.Load() != before {
		t.Fatalf("authority called during drain: %d -> %d", before, a.calls.Load())
	}
}

func TestDelayedExtraAuthFrameIsRejected(t *testing.T) {
	s, err := New(wireAuthority{}, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	st, err := h.NewStream(ctx, s.Host.ID(), AuthProtocol)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.Write(frame(`{"accessToken":"authorized"}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err = st.Write(frame(`{"accessToken":"authorized"}`)); err != nil {
		t.Fatal(err)
	}
	_ = st.CloseWrite()
	_, err = readSize(st)
	if err == nil || s.ActiveSessions() != 0 {
		t.Fatalf("extra frame accepted: read=%v sessions=%d", err, s.ActiveSessions())
	}
}

func TestRelayAuthConnectionRateRejectsAndRecovers(t *testing.T) {
	s, err := New(wireAuthority{}, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if response := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
			t.Fatalf("burst admission: %s", response)
		}
	}
	if response := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(response, `"code":"rate_limited"`) {
		t.Fatalf("burst overload: %s", response)
	}
	time.Sleep(1100 * time.Millisecond)
	if response := authResponse(t, ctx, h, s.Host.ID(), "authorized"); !strings.Contains(response, `"ok":true`) {
		t.Fatalf("rate recovery: %s", response)
	}
}

func TestInitialAuthErrorsAreDistinctAndCloseConnection(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    Authority
		c    Accounting
		code string
	}{
		{"invalid token", failingAuthority{auth.ErrInvalidAccess}, wireAccounting{}, "authentication_failed"},
		{"database outage", failingAuthority{errors.New("database unavailable")}, wireAccounting{}, "temporarily_unavailable"},
		{"exhausted allowance", wireAuthority{}, failingAccounting{quota.ErrExhausted}, "quota_exhausted"},
		{"uncertain accounting", wireAuthority{}, failingAccounting{quota.ErrTemporary}, "temporarily_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(tc.a, tc.c, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
			if err = h.Connect(ctx, ai); err != nil {
				t.Fatal(err)
			}
			resp := authResponse(t, ctx, h, s.Host.ID(), "authorized")
			if !strings.Contains(resp, `"ok":false`) || !strings.Contains(resp, tc.code) {
				t.Fatalf("response %s", resp)
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) && len(h.Network().ConnsToPeer(s.Host.ID())) > 0 {
				time.Sleep(10 * time.Millisecond)
			}
			if n := len(h.Network().ConnsToPeer(s.Host.ID())); n != 0 {
				t.Fatalf("failed initial auth retained %d connections", n)
			}
		})
	}
}

type attemptAuthority struct{ unitCalls chan struct{} }

func (a *attemptAuthority) AuthenticateRelay(_ context.Context, token string) (auth.RelayCredential, error) {
	select {
	case a.unitCalls <- struct{}{}:
	default:
	}
	lifetime := 5 * time.Minute
	switch token {
	case "older":
		lifetime = 2 * time.Minute
	case "newer":
		lifetime = 8 * time.Minute
	}
	return auth.RelayCredential{AccountID: "a", SessionLimit: 2, ExpiresAt: time.Now().Add(lifetime)}, nil
}

func TestOlderAuthenticationCannotRetireOrOverwriteNewerAttempt(t *testing.T) {
	accounting := &blockingEligibility{entered: make(chan struct{}), release: make(chan struct{})}
	authority := &attemptAuthority{unitCalls: make(chan struct{}, 8)}
	s, err := New(authority, accounting, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if response := authResponse(t, ctx, h, s.Host.ID(), "initial"); !strings.Contains(response, `"ok":true`) {
		t.Fatal(response)
	}
	<-authority.unitCalls
	// Let the per-connection authentication limiter replenish before starting
	// the two controlled renewals. The barriers below, not this interval, order them.
	time.Sleep(time.Second)
	accounting.arm()
	older := make(chan string, 1)
	go func() { response, _ := transportAuthResponse(ctx, h, s.Host.ID(), "older"); older <- response }()
	select {
	case <-accounting.entered:
	case <-ctx.Done():
		t.Fatal("older attempt did not reach quota assessment")
	}
	<-authority.unitCalls
	newer := make(chan string, 1)
	go func() { response, _ := transportAuthResponse(ctx, h, s.Host.ID(), "newer"); newer <- response }()
	select {
	case <-authority.unitCalls:
	case <-ctx.Done():
		t.Fatal("newer attempt did not reach the serving interface")
	}
	close(accounting.release)
	select {
	case response := <-older:
		if !strings.Contains(response, `"code":"temporarily_unavailable"`) {
			t.Fatalf("superseded older attempt response: %s", response)
		}
	case <-ctx.Done():
		t.Fatal("older attempt did not finish after quota release")
	}
	select {
	case response := <-newer:
		if !strings.Contains(response, `"ok":true`) {
			t.Fatalf("newer attempt was retired or overwritten: %s", response)
		}
	case <-ctx.Done():
		t.Fatal("newer attempt did not finish")
	}
	if got := s.AccountSessions("a"); got != 1 {
		t.Fatalf("session count after newer completion=%d, want 1", got)
	}
}

type capturedSessionTimer struct{}

func (capturedSessionTimer) Stop() bool { return true }

func TestStaleRenewalExpiryCallbackKeepsCurrentSession(t *testing.T) {
	s, err := New(twoAuthority{}, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var expiryMu sync.Mutex
	var expiries []func()
	s.sessionManager.scheduleExpiry = func(_ time.Duration, callback func()) sessionTimer {
		expiryMu.Lock()
		defer expiryMu.Unlock()
		expiries = append(expiries, callback)
		return capturedSessionTimer{}
	}
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}
	if err := h.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"a", "a"} {
		if response := authResponse(t, ctx, h, s.Host.ID(), token); !strings.Contains(response, `"ok":true`) {
			t.Fatal(response)
		}
	}
	expiryMu.Lock()
	if len(expiries) != 2 {
		count := len(expiries)
		expiryMu.Unlock()
		t.Fatalf("scheduled expiries=%d, want initial and renewal callbacks", count)
	}
	oldExpiry := expiries[0]
	expiryMu.Unlock()
	oldExpiry()
	if got := s.AccountSessions("a"); got != 1 {
		t.Fatalf("stale expiry removed current session: count=%d", got)
	}
	if _, err := client.Reserve(ctx, h, ai); err != nil {
		t.Fatalf("current session lost permission after stale expiry: %v", err)
	}
}

func TestGrantRevocationClosesOnlySessionsUsingThatGrant(t *testing.T) {
	s, err := New(&grantAuthority{}, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clients := make([]host.Host, 2)
	for i := range clients {
		clients[i], err = libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
		if err != nil {
			t.Fatal(err)
		}
		defer clients[i].Close()
		if err = clients[i].Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
			t.Fatal(err)
		}
	}
	for i, grant := range []string{"grant-a", "grant-b"} {
		if response := authResponse(t, ctx, clients[i], s.Host.ID(), grant); !strings.Contains(response, `"ok":true`) {
			t.Fatalf("authenticate grant %s: %s", grant, response)
		}
	}

	invalidateGrantForTest(t, s, "grant-a")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(clients[0].Network().ConnsToPeer(s.Host.ID())) != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(clients[0].Network().ConnsToPeer(s.Host.ID())); got != 0 {
		t.Fatalf("revoked grant retained %d physical connections", got)
	}
	if got := s.AccountSessions("a"); got != 1 {
		t.Fatalf("sessions after grant revocation=%d, want unrelated grant's 1", got)
	}
	if got := len(clients[1].Network().ConnsToPeer(s.Host.ID())); got != 1 {
		t.Fatalf("unrelated grant retained %d connections, want 1", got)
	}
}

func invalidateGrantForTest(t *testing.T, s *Server, grantID string) {
	t.Helper()
	if err := s.RetireGrant(grantID); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteGrantInvalidation(grantID); err != nil {
		t.Fatal(err)
	}
}

func invalidateAccountForTest(t *testing.T, s *Server, accountID string) {
	t.Helper()
	if err := s.RetireAccount(accountID, "account_access_changed"); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAccountInvalidation(accountID); err != nil {
		t.Fatal(err)
	}
}

type blockingGrantAuthority struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *blockingGrantAuthority) AuthenticateRelay(_ context.Context, token string) (auth.RelayCredential, error) {
	a.once.Do(func() {
		close(a.entered)
		<-a.release // Deliberately ignore cancellation to model a late DB result.
	})
	return auth.RelayCredential{AccountID: "a", GrantID: token, SessionLimit: 2, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

func TestGrantRevocationRetiresValidationThatReturnsAfterNotification(t *testing.T) {
	authority := &blockingGrantAuthority{entered: make(chan struct{}), release: make(chan struct{})}
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	result := make(chan string, 1)
	go func() {
		response, _ := transportAuthResponse(ctx, h, s.Host.ID(), "grant-a")
		result <- response
	}()
	select {
	case <-authority.entered:
	case <-ctx.Done():
		t.Fatal("authentication did not enter token validation")
	}
	invalidateGrantForTest(t, s, "grant-a")
	close(authority.release)
	select {
	case response := <-result:
		if !strings.Contains(response, `"code":"temporarily_unavailable"`) {
			t.Fatalf("late validation result applied after revocation: %s", response)
		}
	case <-ctx.Done():
		t.Fatal("late validation did not finish")
	}
	if got := s.AccountSessions("a"); got != 0 {
		t.Fatalf("sessions after revocation=%d, want 0", got)
	}
}

func TestAccountInvalidationRetiresValidationThatReturnsAfterNotification(t *testing.T) {
	authority := &blockingGrantAuthority{entered: make(chan struct{}), release: make(chan struct{})}
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	result := make(chan string, 1)
	go func() {
		response, _ := transportAuthResponse(ctx, h, s.Host.ID(), "grant-a")
		result <- response
	}()
	select {
	case <-authority.entered:
	case <-ctx.Done():
		t.Fatal("authentication did not enter token validation")
	}
	invalidateAccountForTest(t, s, "a")
	close(authority.release)
	select {
	case response := <-result:
		if !strings.Contains(response, `"code":"temporarily_unavailable"`) {
			t.Fatalf("late validation result applied after account invalidation: %s", response)
		}
	case <-ctx.Done():
		t.Fatal("late validation did not finish")
	}
	if got := s.AccountSessions("a"); got != 0 {
		t.Fatalf("sessions after account invalidation=%d, want 0", got)
	}
}

func TestGrantRevocationRetiresAuthenticationDuringInitialQuotaAssessment(t *testing.T) {
	accounting := &blockingEligibility{entered: make(chan struct{}), release: make(chan struct{})}
	s, err := New(&grantAuthority{}, accounting, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	accounting.arm()
	result := make(chan string, 1)
	go func() {
		response, _ := transportAuthResponse(ctx, h, s.Host.ID(), "grant-a")
		result <- response
	}()
	select {
	case <-accounting.entered:
	case <-ctx.Done():
		t.Fatal("authentication did not enter quota assessment")
	}
	invalidateGrantForTest(t, s, "grant-a")
	close(accounting.release)
	select {
	case response := <-result:
		if !strings.Contains(response, `"code":"temporarily_unavailable"`) {
			t.Fatalf("quota-pending result applied after revocation: %s", response)
		}
	case <-ctx.Done():
		t.Fatal("quota-pending authentication did not finish")
	}
	if got := s.AccountSessions("a"); got != 0 {
		t.Fatalf("sessions after revocation=%d, want 0", got)
	}
}

type grantAuthority struct{ wireAuthority }

func (*grantAuthority) AuthenticateRelay(_ context.Context, token string) (auth.RelayCredential, error) {
	return auth.RelayCredential{AccountID: "a", GrantID: token, SessionLimit: 2, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
}

func TestDisconnectedConnectionCannotAuthenticateAfterQuotaCompletes(t *testing.T) {
	accounting := &blockingEligibility{entered: make(chan struct{}), release: make(chan struct{})}
	s, err := New(wireAuthority{}, accounting, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err = h.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	accounting.arm()
	result := make(chan string, 1)
	go func() {
		response, _ := transportAuthResponse(ctx, h, s.Host.ID(), "authorized")
		result <- response
	}()
	select {
	case <-accounting.entered:
	case <-ctx.Done():
		t.Fatal("authentication did not reach initial quota assessment")
	}
	if err = h.Network().ClosePeer(s.Host.ID()); err != nil {
		t.Fatal(err)
	}
	for s.Host.Network().Connectedness(h.ID()) != network.NotConnected {
		select {
		case <-ctx.Done():
			t.Fatal("relay did not observe physical disconnect before quota release")
		case <-time.After(10 * time.Millisecond):
		}
	}
	close(accounting.release)
	select {
	case <-result:
	case <-ctx.Done():
		t.Fatal("authentication did not finish after quota release")
	}
	if got := s.ActiveSessions(); got != 0 {
		t.Fatalf("disconnected authentication result installed a Relay Session: %d", got)
	}
}

func TestFailedRenewalAndAccountChangePreservePriorSession(t *testing.T) {
	s, err := New(twoAuthority{}, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err = h.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	if resp := authResponse(t, ctx, h, s.Host.ID(), "a"); !strings.Contains(resp, `"ok":true`) {
		t.Fatal(resp)
	}
	if resp := authResponse(t, ctx, h, s.Host.ID(), "invalid"); !strings.Contains(resp, "authentication_failed") {
		t.Fatal(resp)
	}
	time.Sleep(time.Second)
	if resp := authResponse(t, ctx, h, s.Host.ID(), "b"); !strings.Contains(resp, "account_change_requires_new_connection") {
		t.Fatal(resp)
	}
	if s.AccountSessions("a") != 1 || s.AccountSessions("b") != 0 {
		t.Fatal("failed renewal changed ownership")
	}
	if _, err = client.Reserve(ctx, h, ai); err != nil {
		t.Fatalf("old permission lost after failed renewal: %v", err)
	}
}

func (c failingAccounting) Observe(string, int64) error              { return c.err }
func (c failingAccounting) AccountingFailed(string)                  {}
func (c failingAccounting) AccountingReady(string) bool              { return true }
func (c failingAccounting) AccountingCurrent(string, uint64) bool    { return true }
func (c failingAccounting) AccountingSessions(string, int)           {}
func (c failingAccounting) ClosureCurrent(quota.ClosureOutcome) bool { return true }
func (c failingAccounting) FlushAllAccounting(context.Context) error { return nil }

func TestGrantRevocationKeepsDelayedClosingConnectionTerminal(t *testing.T) {
	s, err := New(&grantAuthority{}, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if response := authResponse(t, ctx, h, s.Host.ID(), "grant-a"); !strings.Contains(response, `"ok":true`) {
		t.Fatalf("initial auth: %s", response)
	}

	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClose) }) }
	s.sessionManager.physicalClose = func(c network.Conn) {
		close(closeStarted)
		<-releaseClose
		_ = c.Close()
	}
	defer release()
	if err := s.RetireGrant("grant-a"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closeStarted:
		t.Fatal("physical close started before Auth released protection")
	default:
	}
	if got := s.ActiveSessions(); got != 0 {
		t.Fatalf("grant retirement left %d authorized sessions", got)
	}
	if err := s.CompleteGrantInvalidation("grant-a"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closeStarted:
	case <-ctx.Done():
		t.Fatal("grant completion did not start manager-owned physical close")
	}

	// A physical connection whose logical permission has been revoked must
	// remain terminal while Close is delayed. The test authority deliberately
	// accepts the same grant token again to isolate the Relay Session barrier.
	response := authResponse(t, ctx, h, s.Host.ID(), "grant-a")
	if strings.Contains(response, `"ok":true`) {
		t.Fatalf("reauthenticated on a connection while revoked grant close was delayed: %s", response)
	}
	release()
}

func TestAccountRetirementDefersPhysicalCloseUntilCompletion(t *testing.T) {
	s, err := New(&grantAuthority{}, wireAccounting{}, Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
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
	if err = h.Connect(ctx, peer.AddrInfo{ID: s.Host.ID(), Addrs: s.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	if response := authResponse(t, ctx, h, s.Host.ID(), "grant-a"); !strings.Contains(response, `"ok":true`) {
		t.Fatalf("initial auth: %s", response)
	}

	closeStarted := make(chan struct{})
	releaseClose := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClose) }) }
	s.sessionManager.physicalClose = func(c network.Conn) {
		close(closeStarted)
		<-releaseClose
		_ = c.Close()
	}
	defer release()
	if err := s.RetireAccount("a", "policy_changed"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closeStarted:
		t.Fatal("physical close started before account protection was released")
	default:
	}
	if got := s.ActiveSessions(); got != 0 {
		t.Fatalf("account retirement left %d authorized sessions", got)
	}
	if err := s.CompleteAccountInvalidation("a"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closeStarted:
	case <-ctx.Done():
		t.Fatal("account completion did not start manager-owned physical close")
	}
	release()
}
