package auth_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/persistence/postgres"
	"clipp-relay/internal/quota"
	"clipp-relay/internal/relay"
	"github.com/jackc/pgx/v5"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	circuitclient "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
)

type protectedPortal struct {
	provider              *providerDouble
	config                config.Config
	auth                  *auth.Server
	relay                 *relay.Server
	quota                 *quota.Quota
	admin, owner          *http.Cookie
	account, access, csrf string
}

func protectedPortalFixture(t *testing.T) protectedPortal {
	t.Helper()
	return protectedPortalFixtureWithAccounting(t, nil)
}

func protectedPortalFixtureWithAccounting(t *testing.T, configure func(*quota.Quota)) protectedPortal {
	t.Helper()
	c, m, db := fixture(t)
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["protected-admin@gmail.com"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := newProvider(t)
	s := auth.NewWithRuntime(db, c, m, d.endpoints())
	if err := s.EnableNativeOAuth(m.NativeSigningKey); err != nil {
		t.Fatal(err)
	}
	q := quota.New(postgres.New(db))
	if configure != nil {
		configure(q)
	}
	t.Cleanup(q.Close)
	r, err := relay.NewProtected(s, q, relay.Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	admin := loginAs(t, s, d, "protected-admin-"+suffix, "protected-admin@gmail.com", "")
	owner := loginAs(t, s, d, "protected-owner-"+suffix, "protected-owner@example.test", "")
	var id string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, "protected-owner-"+suffix).Scan(&id); err != nil {
		t.Fatal(err)
	}
	p := protectedPortal{provider: d, config: c, auth: s, relay: r, quota: q, admin: admin, owner: owner, account: id, csrf: csrfFrom(t, s, admin)}
	p.mutate(t, "approve", "1", nil)
	p.access, _ = redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, authorizeClient(t, s, owner, "android", c.PublicClients.AndroidRedirect))
	return p
}

func (p protectedPortal) mutate(t *testing.T, action, revision string, extra url.Values) {
	t.Helper()
	form := url.Values{"csrf": {p.csrf}, "reason": {"routine_administration"}, "action": {action}, "revision": {revision}, "plan_id": {"6dd09395-51a0-451c-96b3-716e6038e870"}}
	for k, v := range extra {
		form[k] = v
	}
	if out := portalRequest(p.auth, "POST", "/admin/accounts/"+p.account, p.admin, form, p.config.PortalOrigin); out.Code != 303 {
		t.Fatalf("%s: %d %s", action, out.Code, out.Body.String())
	}
}

func protectedClient(t *testing.T, p protectedPortal, options ...libp2p.Option) host.Host {
	t.Helper()
	options = append(options, libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	h, err := libp2p.New(options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Connect(ctx, peer.AddrInfo{ID: p.relay.Host.ID(), Addrs: p.relay.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	return h
}

func protectedAuthenticate(ctx context.Context, h host.Host, p protectedPortal) (string, error) {
	return protectedAuthenticateToken(ctx, h, p, p.access)
}

func protectedAuthenticateToken(ctx context.Context, h host.Host, p protectedPortal, token string) (string, error) {
	st, err := h.NewStream(ctx, p.relay.Host.ID(), relay.AuthProtocol)
	if err != nil {
		return "", err
	}
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(5 * time.Second))
	body := []byte(fmt.Sprintf(`{"accessToken":%q}`, token))
	var header [10]byte
	n := binary.PutUvarint(header[:], uint64(len(body)))
	if _, err = st.Write(append(header[:n], body...)); err != nil {
		return "", err
	}
	if err = st.CloseWrite(); err != nil {
		return "", err
	}
	size, err := binary.ReadUvarint(testByteReader{st})
	if err != nil {
		return "", err
	}
	if size > 4096 {
		return "", fmt.Errorf("response too large: %d", size)
	}
	response := make([]byte, size)
	_, err = io.ReadFull(st, response)
	return string(response), err
}

func TestRefreshReplayInvalidatesOnlyOwningLiveRelayGrant(t *testing.T) {
	p := protectedPortalFixture(t)
	accessA, refreshA := redeemClient(t, p.auth, "android", p.config.PublicClients.AndroidRedirect, authorizeClient(t, p.auth, p.owner, "android", p.config.PublicClients.AndroidRedirect))
	accessB, _ := redeemClient(t, p.auth, "android", p.config.PublicClients.AndroidRedirect, authorizeClient(t, p.auth, p.owner, "android", p.config.PublicClients.AndroidRedirect))
	credentialA, err := p.auth.AuthenticateRelay(context.Background(), accessA)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	initial, err := p.quota.Eligibility(ctx, p.account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.quota.Settle(ctx, quota.Settlement{AccountID: p.account, WeekStart: initial.WeekStart, Sequence: 1, DeltaBytes: 123, OperationID: "5f29fe63-8676-49e0-b918-f792896323f0"}); err != nil {
		t.Fatalf("seed recorded usage: %v", err)
	}
	if err = p.quota.Observe(p.account, 55); err != nil {
		t.Fatalf("seed pending usage: %v", err)
	}
	usageBefore, err := p.quota.Eligibility(ctx, p.account)
	if err != nil || usageBefore.RecordedBytes != 123 || usageBefore.EffectiveBytes != 178 {
		t.Fatalf("seed quota usage=%+v err=%v", usageBefore, err)
	}
	grantA, grantB := p, p
	grantA.access, grantB.access = accessA, accessB
	clientA, clientB := protectedClient(t, p), protectedClient(t, p)
	requireProtectedAuthentication(t, clientA, grantA, `"ok":true`)
	requireProtectedAuthentication(t, clientB, grantB, `"ok":true`)
	if got := p.relay.AccountSessions(p.account); got != 2 {
		t.Fatalf("sessions before replay=%d, want 2", got)
	}
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refreshA}}
	if rotated := nativeTokenPost(t, p.auth.Handler(), form); rotated.Code != http.StatusOK {
		t.Fatalf("ordinary refresh status=%d %s", rotated.Code, rotated.Body.String())
	}
	if got := p.relay.AccountSessions(p.account); got != 2 {
		t.Fatalf("ordinary refresh invalidated a Relay Session: %d", got)
	}
	p.auth.SetCredentialCommitHookForTest(func(context.Context, pgx.Tx) error {
		return errors.New("simulated refresh replay rollback")
	})
	rolledBack := nativeTokenPost(t, p.auth.Handler(), form)
	p.auth.SetCredentialCommitHookForTest(nil)
	if rolledBack.Code != http.StatusServiceUnavailable {
		t.Fatalf("confirmed replay rollback status=%d %s", rolledBack.Code, rolledBack.Body.String())
	}
	if got := p.relay.AccountSessions(p.account); got != 2 {
		t.Fatalf("rolled-back replay sessions=%d, want 2", got)
	}
	if _, err := p.auth.AuthenticateRelay(context.Background(), accessA); err != nil {
		t.Fatalf("confirmed rollback revoked its grant: %v", err)
	}
	// Simulate a wrapper failure. The composed direct manager retry must still
	// retire the owning grant before the committed replay returns.
	p.auth.SetGrantRevokedForTest(func(string) error { return errors.New("simulated notification wrapper failure") })
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	p.auth.SetCredentialCommitHookForTest(func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		cancelRequest() // Model request cancellation after the database commit.
		return errors.New("simulated lost refresh replay commit acknowledgment")
	})
	replayRequest := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode())).WithContext(requestCtx)
	replayRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	replay := httptest.NewRecorder()
	p.auth.Handler().ServeHTTP(replay, replayRequest)
	cancelRequest()
	p.auth.SetCredentialCommitHookForTest(nil)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("confirmed refresh replay status=%d %s", replay.Code, replay.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(clientA.Network().ConnsToPeer(p.relay.Host.ID())) != 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := len(clientA.Network().ConnsToPeer(p.relay.Host.ID())); got != 0 {
		t.Fatalf("revoked grant retained %d physical connections", got)
	}
	if got := p.relay.AccountSessions(p.account); got != 1 {
		t.Fatalf("sessions after replay=%d, want unrelated grant's 1", got)
	}
	if got := len(clientB.Network().ConnsToPeer(p.relay.Host.ID())); got != 1 {
		t.Fatalf("unrelated grant retained %d physical connections, want 1", got)
	}
	// Duplicate delivery is harmless and cannot disturb the unrelated grant.
	if err := p.relay.InvalidateGrant(credentialA.GrantID); err != nil {
		t.Fatal(err)
	}
	if err := p.relay.InvalidateGrant(credentialA.GrantID); err != nil {
		t.Fatal(err)
	}
	if got := p.relay.AccountSessions(p.account); got != 1 || len(clientB.Network().ConnsToPeer(p.relay.Host.ID())) != 1 {
		t.Fatalf("duplicate grant delivery affected unrelated session: sessions=%d connections=%d", got, len(clientB.Network().ConnsToPeer(p.relay.Host.ID())))
	}
	if _, err := p.auth.AuthenticateRelay(context.Background(), accessB); err != nil {
		t.Fatalf("replay invalidated unrelated grant's access token: %v", err)
	}
	if _, err := p.auth.AuthenticateRelay(context.Background(), accessA); err == nil {
		t.Fatal("replay left the owning grant's access token valid")
	}
	if !p.quota.AccountingReady(p.account) {
		t.Fatal("grant replay marked account quota unhealthy")
	}
	usageAfter, err := p.quota.Eligibility(ctx, p.account)
	if err != nil || usageAfter.RecordedBytes != usageBefore.RecordedBytes || usageAfter.EffectiveBytes != usageBefore.EffectiveBytes || usageAfter.CapBytes != usageBefore.CapBytes {
		t.Fatalf("grant replay changed account quota: before=%+v after=%+v err=%v", usageBefore, usageAfter, err)
	}
}

func requireProtectedAuthentication(t *testing.T, h host.Host, p protectedPortal, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := protectedAuthenticate(ctx, h, p)
	if err != nil || !strings.Contains(response, want) {
		t.Fatalf("authentication=%s error=%v; want %s", response, err, want)
	}
}

func TestProductionProtectionClosesAllOnSessionCapReduction(t *testing.T) {
	t.Run("observed", func(t *testing.T) { testProtectedSessionCap(t) })
}

func TestProductionProtectionReadsCurrentCapAfterAuthReturns(t *testing.T) {
	p := protectedPortalFixture(t)
	p.mutate(t, "override", "2", url.Values{"sessions_override": {"5"}})
	clients := make([]host.Host, 4)
	for i := range clients {
		clients[i] = protectedClient(t, p)
	}
	for i := 0; i < 3; i++ {
		requireProtectedAuthentication(t, clients[i], p, `"ok":true`)
	}
	if got := p.relay.AccountSessions(p.account); got != 3 {
		t.Fatalf("initial sessions=%d, want 3", got)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	p.quota.SetAccountingBoundariesForTest(nil, nil, func(ctx context.Context) error {
		once.Do(func() { close(entered); <-release })
		return ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := make(chan string, 1)
	go func() {
		response, err := protectedAuthenticate(ctx, clients[3], p)
		result <- fmt.Sprintf("%s %v", response, err)
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("authentication did not reach pending Quota assessment")
	}
	// The pending assessment is ordered after Auth has already returned its
	// credential, whose SessionLimit still reflects the old policy.
	p.mutate(t, "override", "3", url.Values{"sessions_override": {"3"}})
	close(release)
	select {
	case response := <-result:
		if !strings.Contains(response, `"code":"session_limit_exceeded"`) {
			t.Fatalf("current database cap response=%s", response)
		}
	case <-ctx.Done():
		t.Fatal("pending authentication did not finish")
	}
	if got := p.relay.AccountSessions(p.account); got != 3 {
		t.Fatalf("sessions after current cap assessment=%d, want 3", got)
	}
}

func testProtectedSessionCap(t *testing.T) {
	p := protectedPortalFixture(t)
	p.mutate(t, "override", "2", url.Values{"sessions_override": {"2"}})
	first, second := protectedClient(t, p), protectedClient(t, p)
	requireProtectedAuthentication(t, first, p, `"ok":true`)
	requireProtectedAuthentication(t, second, p, `"ok":true`)
	p.mutate(t, "override", "3", url.Values{"sessions_override": {"1"}})
	if got := p.relay.AccountSessions(p.account); got != 0 {
		t.Fatalf("reduced cap retained %d sessions", got)
	}
	until := time.Now().Add(time.Second)
	for (len(first.Network().ConnsToPeer(p.relay.Host.ID())) != 0 || len(second.Network().ConnsToPeer(p.relay.Host.ID())) != 0) && time.Now().Before(until) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(first.Network().ConnsToPeer(p.relay.Host.ID())) != 0 || len(second.Network().ConnsToPeer(p.relay.Host.ID())) != 0 {
		t.Fatal("cap reduction retained physical connection")
	}
	first, second = protectedClient(t, p), protectedClient(t, p)
	requireProtectedAuthentication(t, first, p, `"ok":true`)
	requireProtectedAuthentication(t, second, p, "session_limit_exceeded")
	// A replacement of the same Peer ID consumes its existing slot atomically.
	replacement := protectedClient(t, p, libp2p.Identity(first.Peerstore().PrivKey(first.ID())))
	requireProtectedAuthentication(t, replacement, p, `"ok":true`)
	if got := p.relay.AccountSessions(p.account); got != 1 {
		t.Fatalf("replacement sessions=%d", got)
	}
}

func TestProductionProtectionInvalidationDefeatsInFlightAuthentication(t *testing.T) {
	t.Run("observed", func(t *testing.T) { testProtectedInvalidation(t) })
}
func testProtectedInvalidation(t *testing.T) {
	p := protectedPortalFixture(t)
	h := protectedClient(t, p)
	entered, release := make(chan struct{}), make(chan struct{})
	releaseAuth := sync.OnceFunc(func() { close(release) })
	defer releaseAuth()
	var once sync.Once
	p.auth.SetRelayCredentialHookForTest(func() { once.Do(func() { close(entered); <-release }) })
	defer p.auth.SetRelayCredentialHookForTest(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan string, 1)
	go func() {
		response, err := protectedAuthenticate(ctx, h, p)
		result <- fmt.Sprintf("%s %v", response, err)
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("credential lookup did not start")
	}
	p.mutate(t, "revoke", "2", nil)
	releaseAuth()
	select {
	case response := <-result:
		if !strings.Contains(response, `"code":"temporarily_unavailable"`) {
			t.Fatalf("account invalidation did not cancel the in-flight attempt safely: %s", response)
		}
	case <-ctx.Done():
		t.Fatal("authentication did not finish")
	}
	if got := p.relay.AccountSessions(p.account); got != 0 {
		t.Fatalf("invalidation retained %d sessions", got)
	}
}

// Cross-account replacement must protect both accounts and leave the original
// physical session authoritative when the target account has no free slot.
func TestProductionProtectionCrossAccountReplacementAtCapacity(t *testing.T) {
	p := protectedPortalFixture(t)
	p.mutate(t, "override", "2", url.Values{"sessions_override": {"1"}})
	original := protectedClient(t, p)
	requireProtectedAuthentication(t, original, p, `"ok":true`)
	subject := "replacement-target-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	owner := loginAs(t, p.auth, p.provider, subject, "replacement@example.test", "")
	var id string
	if err := p.auth.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	target := p
	target.account, target.owner = id, owner
	target.mutate(t, "approve", "1", nil)
	target.mutate(t, "override", "2", url.Values{"sessions_override": {"1"}})
	target.access, _ = redeemClient(t, p.auth, "android", p.config.PublicClients.AndroidRedirect, authorizeClient(t, p.auth, owner, "android", p.config.PublicClients.AndroidRedirect))
	occupied := protectedClient(t, target)
	requireProtectedAuthentication(t, occupied, target, `"ok":true`)
	replacement := protectedClient(t, p, libp2p.Identity(original.Peerstore().PrivKey(original.ID())))
	requireProtectedAuthentication(t, replacement, target, "session_limit_exceeded")
	if p.relay.AccountSessions(p.account) != 1 || p.relay.AccountSessions(target.account) != 1 || len(original.Network().ConnsToPeer(p.relay.Host.ID())) != 1 {
		t.Fatal("failed cross-account replacement displaced admitted authority")
	}
	_ = occupied.Close()
	until := time.Now().Add(time.Second)
	for p.relay.AccountSessions(target.account) != 0 && time.Now().Before(until) {
		time.Sleep(5 * time.Millisecond)
	}
	requireProtectedAuthentication(t, replacement, target, `"ok":true`)
	if p.relay.AccountSessions(p.account) != 0 || p.relay.AccountSessions(target.account) != 1 {
		t.Fatal("cross-account replacement did not transfer the physical session")
	}
	until = time.Now().Add(time.Second)
	for len(original.Network().ConnsToPeer(p.relay.Host.ID())) != 0 && time.Now().Before(until) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(original.Network().ConnsToPeer(p.relay.Host.ID())) != 0 {
		t.Fatal("cross-account replacement left original physical connection open")
	}
}

func TestProductionProtectionComposesObservedSettlement(t *testing.T) {
	p := protectedPortalFixture(t)
	now := time.Now().UTC()
	days := (int(now.Weekday()) + 6) % 7
	week := time.Date(now.Year(), now.Month(), now.Day()-days, 0, 0, 0, 0, time.UTC)
	op, err := quota.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.quota.Settle(context.Background(), quota.Settlement{AccountID: p.account, WeekStart: week, OperationID: op, Sequence: 1, DeltaBytes: 123456})
	if err != nil || result.RecordedBytes != 123456 {
		t.Fatalf("composed settlement=%+v %v", result, err)
	}
}

func TestProductionObservedReporterSharedExhaustionClosesEverySession(t *testing.T) {
	p := protectedPortalFixture(t)
	p.mutate(t, "override", "2", url.Values{"weekly_bytes_override": {"10000"}, "sessions_override": {"2"}})
	first, second := protectedClient(t, p), protectedClient(t, p)
	requireProtectedAuthentication(t, first, p, `"ok":true`)
	requireProtectedAuthentication(t, second, p, `"ok":true`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := circuitclient.Reserve(ctx, first, peer.AddrInfo{ID: p.relay.Host.ID(), Addrs: p.relay.Host.Addrs()}); err != nil {
		t.Fatal(err)
	}
	// Stock HOP protocol callbacks must reach the observed accounting path.
	now := time.Now().UTC()
	days := (int(now.Weekday()) + 6) % 7
	week := time.Date(now.Year(), now.Month(), now.Day()-days, 0, 0, 0, 0, time.UTC)
	until := time.Now().Add(2500 * time.Millisecond)
	var recorded quota.RecordedUsage
	for time.Now().Before(until) {
		recorded, _ = p.quota.RecordedUsage(ctx, p.account, week)
		if recorded.RecordedBytes > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if recorded.RecordedBytes <= 0 {
		t.Fatal("stock HOP reporter did not persist usage")
	}
	if err := p.quota.Observe(p.account, 20000); err != nil {
		t.Fatal(err)
	}
	until = time.Now().Add(2500 * time.Millisecond)
	for p.relay.AccountSessions(p.account) != 0 && time.Now().Before(until) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := p.relay.AccountSessions(p.account); got != 0 {
		t.Fatalf("exhaustion retained %d account sessions", got)
	}
	recorded, err := p.quota.RecordedUsage(ctx, p.account, week)
	if err != nil || recorded.RecordedBytes < 20000 {
		t.Fatalf("full overrun=%+v %v", recorded, err)
	}
	until = time.Now().Add(time.Second)
	for (len(first.Network().ConnsToPeer(p.relay.Host.ID())) > 0 || len(second.Network().ConnsToPeer(p.relay.Host.ID())) > 0) && time.Now().Before(until) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(first.Network().ConnsToPeer(p.relay.Host.ID())) > 0 || len(second.Network().ConnsToPeer(p.relay.Host.ID())) > 0 {
		t.Fatal("exhausted physical connections remain open")
	}
}

func TestProductionObservedPolicyEditsPreservePendingAndEnforceEquality(t *testing.T) {
	p := protectedPortalFixture(t)
	first := protectedClient(t, p)
	requireProtectedAuthentication(t, first, p, `"ok":true`)
	if err := p.quota.Observe(p.account, 60); err != nil {
		t.Fatal(err)
	}
	p.mutate(t, "override", "2", url.Values{"weekly_bytes_override": {"60"}, "sessions_override": {"2"}})
	if got := p.relay.AccountSessions(p.account); got != 0 {
		t.Fatalf("cap equality retained %d sessions", got)
	}
	p.mutate(t, "override", "3", url.Values{"weekly_bytes_override": {"100"}})
	replacement := protectedClient(t, p)
	requireProtectedAuthentication(t, replacement, p, `"ok":true`)
	if err := p.quota.FlushAccounting(context.Background(), p.account); err != nil && err != quota.ErrTemporary {
		t.Fatal(err)
	}
	eligible, err := p.quota.Eligibility(context.Background(), p.account)
	if err != nil || eligible.EffectiveBytes != 60 {
		t.Fatalf("policy edit reset consumption: %+v %v", eligible, err)
	}
	p.mutate(t, "override", "4", url.Values{"weekly_bytes_override": {"59"}})
	if got := p.relay.AccountSessions(p.account); got != 0 {
		t.Fatalf("cap reduction retained %d sessions", got)
	}
}

func TestProductionObservedSuspensionRetainsUsage(t *testing.T) {
	p := protectedPortalFixture(t)
	first := protectedClient(t, p)
	requireProtectedAuthentication(t, first, p, `"ok":true`)
	if err := p.quota.Observe(p.account, 83); err != nil {
		t.Fatal(err)
	}
	p.mutate(t, "suspend", "2", nil)
	if got := p.relay.AccountSessions(p.account); got != 0 {
		t.Fatalf("suspension retained %d sessions", got)
	}
	if err := p.quota.FlushAccounting(context.Background(), p.account); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	days := (int(now.Weekday()) + 6) % 7
	week := time.Date(now.Year(), now.Month(), now.Day()-days, 0, 0, 0, 0, time.UTC)
	usage, err := p.quota.RecordedUsage(context.Background(), p.account, week)
	if err != nil || usage.RecordedBytes != 83 {
		t.Fatalf("suspension lost usage: %+v %v", usage, err)
	}
	if _, err := p.quota.Eligibility(context.Background(), p.account); err != nil {
		t.Fatalf("suspension made quota unavailable: %v", err)
	}
	if !p.quota.AccountingReady(p.account) {
		t.Fatal("suspension marked accounting unhealthy")
	}
	replacement := protectedClient(t, p)
	requireProtectedAuthentication(t, replacement, p, "authentication_failed")
}

func TestOwnerAccountRevocationInvalidatesOnlyThatAccountsSessions(t *testing.T) {
	p := protectedPortalFixture(t)
	usage, err := p.quota.Eligibility(context.Background(), p.account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.quota.Settle(context.Background(), quota.Settlement{AccountID: p.account, WeekStart: usage.WeekStart, Sequence: 1, DeltaBytes: 123, OperationID: "a550e1a9-755e-4cba-8e87-a36da25d1d98"}); err != nil {
		t.Fatal(err)
	}
	if err := p.quota.Observe(p.account, 55); err != nil {
		t.Fatal(err)
	}
	usageBefore, err := p.quota.Eligibility(context.Background(), p.account)
	if err != nil || usageBefore.RecordedBytes != 123 || usageBefore.EffectiveBytes != 178 {
		t.Fatalf("seed usage before revocation=%+v err=%v", usageBefore, err)
	}
	accessAgain, _ := redeemClient(t, p.auth, "android", p.config.PublicClients.AndroidRedirect,
		authorizeClient(t, p.auth, p.owner, "android", p.config.PublicClients.AndroidRedirect))
	otherOwner := loginAs(t, p.auth, p.provider, "protected-unrelated-"+strconv.FormatInt(time.Now().UnixNano(), 10), "unrelated@example.test", "")
	var otherAccount string
	if err := p.auth.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE email='unrelated@example.test' ORDER BY created_at DESC LIMIT 1`).Scan(&otherAccount); err != nil {
		t.Fatal(err)
	}
	approve := url.Values{"csrf": {csrfFrom(t, p.auth, p.admin)}, "reason": {"routine_administration"}, "action": {"approve"}, "revision": {"1"}, "plan_id": {"6dd09395-51a0-451c-96b3-716e6038e870"}}
	if out := portalRequest(p.auth, "POST", "/admin/accounts/"+otherAccount, p.admin, approve, p.config.PortalOrigin); out.Code != http.StatusSeeOther {
		t.Fatalf("approve unrelated account: %d %s", out.Code, out.Body.String())
	}
	otherAccess, _ := redeemClient(t, p.auth, "android", p.config.PublicClients.AndroidRedirect,
		authorizeClient(t, p.auth, otherOwner, "android", p.config.PublicClients.AndroidRedirect))
	first, second, unrelated := protectedClient(t, p), protectedClient(t, p), protectedClient(t, p)
	requireProtectedAuthentication(t, first, p, `"ok":true`)
	requireProtectedAuthentication(t, second, protectedPortal{auth: p.auth, relay: p.relay, access: accessAgain}, `"ok":true`)
	other := protectedPortal{auth: p.auth, relay: p.relay, access: otherAccess}
	requireProtectedAuthentication(t, unrelated, other, `"ok":true`)
	if p.relay.AccountSessions(p.account) != 2 || p.relay.AccountSessions(otherAccount) != 1 {
		t.Fatal("fixture did not establish the expected account sessions")
	}
	var delivered atomic.Int32
	p.auth.SetAccountInvalidatedForTest(func(string) error {
		if delivered.Add(1) == 1 {
			return errors.New("simulated notification wrapper failure")
		}
		return nil
	})
	form := url.Values{"csrf": {csrfFrom(t, p.auth, p.owner)}}
	out := portalRequest(p.auth, "POST", "/auth/revoke", p.owner, form, p.config.PortalOrigin)
	if out.Code != http.StatusSeeOther || delivered.Load() != 1 {
		t.Fatalf("owner revocation status=%d deliveries=%d body=%s", out.Code, delivered.Load(), out.Body.String())
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && (p.relay.AccountSessions(p.account) != 0 || len(first.Network().ConnsToPeer(p.relay.Host.ID())) != 0 || len(second.Network().ConnsToPeer(p.relay.Host.ID())) != 0) {
		time.Sleep(5 * time.Millisecond)
	}
	if p.relay.AccountSessions(p.account) != 0 || p.relay.AccountSessions(otherAccount) != 1 {
		t.Fatalf("account revocation sessions: target=%d unrelated=%d", p.relay.AccountSessions(p.account), p.relay.AccountSessions(otherAccount))
	}
	if len(unrelated.Network().ConnsToPeer(p.relay.Host.ID())) != 1 {
		t.Fatal("account revocation closed an unrelated account's connection")
	}
	if _, err := p.auth.AuthenticateRelay(context.Background(), otherAccess); err != nil {
		t.Fatalf("account revocation invalidated unrelated credentials: %v", err)
	}
	usageAfter, err := p.quota.Eligibility(context.Background(), p.account)
	if err != nil || usageAfter.RecordedBytes != usageBefore.RecordedBytes || usageAfter.EffectiveBytes != usageBefore.EffectiveBytes || usageAfter.CapBytes != usageBefore.CapBytes {
		t.Fatalf("account revocation changed allowance or usage: before=%+v after=%+v err=%v", usageBefore, usageAfter, err)
	}
	if !p.quota.AccountingReady(p.account) {
		t.Fatal("account-wide revocation marked accounting unhealthy")
	}
}

func TestSuspensionInvalidatesAuthenticationAfterCredentialValidation(t *testing.T) {
	p := protectedPortalFixture(t)
	h := protectedClient(t, p)
	entered, release := make(chan struct{}), make(chan struct{})
	p.auth.SetRelayCredentialHookForTest(func() {
		close(entered)
		<-release
	})
	result := make(chan string, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, _ := protectedAuthenticate(ctx, h, p)
		result <- response
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("authentication did not reach the returned-but-unapplied credential seam")
	}
	p.mutate(t, "suspend", "2", nil)
	close(release)
	p.auth.SetRelayCredentialHookForTest(nil)
	select {
	case response := <-result:
		if strings.Contains(response, `"ok":true`) {
			t.Fatalf("suspension allowed the late authentication result: %s", response)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late authentication did not finish")
	}
	if got := p.relay.AccountSessions(p.account); got != 0 {
		t.Fatalf("suspension left %d Relay Sessions", got)
	}
}

func TestOwnerRevocationDeliverySurvivesRequestCancellationAfterCommit(t *testing.T) {
	p := protectedPortalFixture(t)
	var subject string
	if err := p.auth.Pool.QueryRow(context.Background(), `SELECT subject FROM public.accounts WHERE id=$1`, p.account).Scan(&subject); err != nil {
		t.Fatal(err)
	}
	owner := loginAs(t, p.auth, p.provider, subject, "protected-owner@example.test", "")
	access, _ := redeemClient(t, p.auth, "android", p.config.PublicClients.AndroidRedirect,
		authorizeClient(t, p.auth, owner, "android", p.config.PublicClients.AndroidRedirect))
	client := protectedClient(t, p)
	grant := protectedPortal{auth: p.auth, relay: p.relay, access: access}
	requireProtectedAuthentication(t, client, grant, `"ok":true`)
	ctx, cancel := context.WithCancel(context.Background())
	p.auth.SetAccountCommitHookForTest(func(commitCtx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(commitCtx); err != nil {
			return err
		}
		cancel()
		return errors.New("lost commit acknowledgment after request cancellation")
	})
	form := url.Values{"csrf": {csrfFrom(t, p.auth, owner)}}
	req := httptest.NewRequest(http.MethodPost, "/auth/revoke", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", p.config.PortalOrigin)
	req.AddCookie(owner)
	response := httptest.NewRecorder()
	p.auth.Handler().ServeHTTP(response, req)
	p.auth.SetAccountCommitHookForTest(nil)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "Result could not be confirmed") {
		t.Fatalf("canceled ambiguous revocation response=%d %s", response.Code, response.Body.String())
	}
	if got := p.relay.AccountSessions(p.account); got != 0 {
		t.Fatalf("canceled request suppressed account invalidation; sessions=%d", got)
	}
}
