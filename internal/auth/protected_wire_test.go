package auth_test

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/quota"
	"clipp-relay/internal/relay"
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
	q := quota.NewWithRuntime(db)
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
	st, err := h.NewStream(ctx, p.relay.Host.ID(), relay.AuthProtocol)
	if err != nil {
		return "", err
	}
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(5 * time.Second))
	body := []byte(fmt.Sprintf(`{"accessToken":%q}`, p.access))
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
		if !strings.Contains(response, "authentication_failed") {
			t.Fatalf("stale credential admitted: %s", response)
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
	eligible, err := p.quota.Eligibility(context.Background(), p.account, 0)
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
	if _, err := p.quota.Eligibility(context.Background(), p.account, 0); err != quota.ErrTemporary {
		t.Fatalf("suspended account eligible: %v", err)
	}
	replacement := protectedClient(t, p)
	requireProtectedAuthentication(t, replacement, p, "authentication_failed")
}
