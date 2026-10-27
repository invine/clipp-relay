package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/persistence/postgres"
)

type blockingResponseWriter struct {
	inner   *httptest.ResponseRecorder
	started chan struct{}
	release chan struct{}
}

func (w *blockingResponseWriter) Header() http.Header { return w.inner.Header() }
func (w *blockingResponseWriter) WriteHeader(status int) {
	select {
	case w.started <- struct{}{}:
	default:
	}
	<-w.release
	w.inner.WriteHeader(status)
}
func (w *blockingResponseWriter) Write(data []byte) (int, error) { return w.inner.Write(data) }

type adminHTTPTestWork struct{ state *adminHTTPTestState }

type adminHTTPTestState struct {
	actorID, targetID string
	csrfHash          []byte
	mutation          persistence.AdminAccountMutation
}

type adminHTTPTestOperation struct{ tx *adminHTTPTestTransaction }

type adminHTTPTestTransaction struct {
	persistence.AccountAccessTransaction
	state *adminHTTPTestState
}

func (w adminHTTPTestWork) Start(context.Context) (persistence.Operation, error) {
	tx := &adminHTTPTestTransaction{state: w.state}
	return adminHTTPTestOperation{tx: tx}, nil
}

func (o adminHTTPTestOperation) Context() context.Context { return context.Background() }
func (adminHTTPTestOperation) Close()                     {}
func (o adminHTTPTestOperation) Begin(context.Context) (persistence.Transaction, error) {
	return o.tx, nil
}
func (o adminHTTPTestOperation) BeginAccountAccess(context.Context) (persistence.AccountAccessTransaction, error) {
	return o.tx, nil
}
func (tx *adminHTTPTestTransaction) LockAdministratorAccounts(context.Context, string, string) error {
	return nil
}
func (tx *adminHTTPTestTransaction) PortalSessionForOwner(context.Context, [32]byte, uint64) (persistence.PortalSessionOwner, error) {
	return persistence.PortalSessionOwner{AccountID: tx.state.actorID, Status: "Active", Email: "admin@example.test", Subject: "admin-subject", Generation: 1, PepperVersion: 1, CSRFHash: tx.state.csrfHash}, nil
}
func (*adminHTTPTestTransaction) TouchPortalSession(context.Context, [32]byte, uint64, string, int64) (bool, error) {
	return true, nil
}
func (*adminHTTPTestTransaction) Commit(context.Context) error   { return nil }
func (*adminHTTPTestTransaction) Rollback(context.Context) error { return nil }
func (*adminHTTPTestTransaction) AdministratorSession(context.Context, string, []byte, uint64) (persistence.AdministratorSession, error) {
	domain := "example.test"
	return persistence.AdministratorSession{Email: "admin@example.test", Verified: true, HostedDomain: &domain, Recent: true}, nil
}
func (tx *adminHTTPTestTransaction) AccountAccessState(_ context.Context, id string, _ bool) (persistence.AccountAccessState, error) {
	if id != tx.state.targetID && id != tx.state.actorID {
		return persistence.AccountAccessState{}, persistence.ErrNotFound
	}
	return persistence.AccountAccessState{ID: id, Status: "Active", Subject: "target-subject", Revision: 1, Generation: 0, WeeklyBytes: 100, Sessions: 3}, nil
}
func (tx *adminHTTPTestTransaction) ApplyAdminAccountMutation(_ context.Context, mutation persistence.AdminAccountMutation) (persistence.AdminAccountMutationResult, error) {
	tx.state.mutation = mutation
	return persistence.AdminAccountMutationResult{WeeklyBytes: 100, Sessions: 2, NewStatus: "Active", NewRevision: 2, NewGeneration: 0}, nil
}

func adminFixture(t *testing.T) (*Server, *http.Cookie, *database.Runtime, string) {
	t.Helper()
	path := os.Getenv("CLIPP_TEST_SERVING_CONFIG")
	if path == "" {
		t.Skip("disposable PostgreSQL fixture not configured")
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Database.Host != "localhost" || !strings.HasPrefix(c.Database.Name, "clipp_ticket02_") {
		t.Fatal("fixture must be disposable")
	}
	m, err := c.ReadMaterial()
	if err != nil {
		t.Fatal(err)
	}
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["consistency@gmail.com"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	pool, err := database.NewPool(context.Background(), c, m, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	runtime := database.NewRuntime(pool)
	s := New(Dependencies{Work: postgres.New(runtime), Deletions: postgres.NewDeletions(runtime), Recovery: postgres.NewRecovery(runtime), ReadViews: postgres.NewReadViews(runtime), Maintenance: postgres.NewMaintenance(runtime)}, c, m, Provider{})
	if err := s.EnableNativeOAuth(m.NativeSigningKey); err != nil {
		t.Fatal(err)
	}
	s.SetAccountChangedForTest(func(AccountChange) string { return "" })
	s.SetGrantRevokedForTest(func(string) error { return nil })
	id, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(context.Background(), `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id) VALUES($1,'https://accounts.google.com',$2,'consistency@gmail.com',true,clock_timestamp(),clock_timestamp(),clock_timestamp(),'Active','6dd09395-51a0-451c-96b3-716e6038e870')`, id, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.portal_sessions WHERE account_id=$1; DELETE FROM public.accounts WHERE id=$1`, id)
	})
	credential, err := opaque()
	if err != nil {
		t.Fatal(err)
	}
	csrf := s.csrf(m.CurrentPepper, credential)
	_, err = pool.Exec(context.Background(), `INSERT INTO public.portal_sessions(credential_digest,pepper_version,account_id,credential_generation,csrf_digest,google_authenticated_at,created_at,last_used_at,idle_expires_at,absolute_expires_at) VALUES($1,$2,$3,0,$4,clock_timestamp(),clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '12 hours')`, s.digest(m.CurrentPepper, "portal-session", credential), m.CurrentPepper, id, s.digest(m.CurrentPepper, "csrf-store", csrf))
	if err != nil {
		t.Fatal(err)
	}
	return s, &http.Cookie{Name: sessionCookie, Value: credential}, database.NewRuntime(pool), c.PortalOrigin
}

func internalRequest(s *Server, method, path string, cookie *http.Cookie, form url.Values, origin string) *httptest.ResponseRecorder {
	var body strings.Reader
	if form != nil {
		body = *strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, path, &body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func nativeAccessForAccount(t *testing.T, s *Server, db *database.Runtime, account string, origin string) (string, *http.Cookie) {
	t.Helper()
	credential, err := opaque()
	if err != nil {
		t.Fatal(err)
	}
	csrf := s.csrf(s.CurrentPepper, credential)
	_, err = db.Pool.Exec(context.Background(), `INSERT INTO public.portal_sessions(credential_digest,pepper_version,account_id,credential_generation,csrf_digest,google_authenticated_at,created_at,last_used_at,idle_expires_at,absolute_expires_at) VALUES($1,$2,$3,0,$4,clock_timestamp(),clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '12 hours')`, s.digest(s.CurrentPepper, "portal-session", credential), s.CurrentPepper, account, s.digest(s.CurrentPepper, "csrf-store", csrf))
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: credential}
	verifier := strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(verifier))
	query := url.Values{"response_type": {"code"}, "client_id": {"android"}, "redirect_uri": {"clipp-relay://oauth/callback"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {"admin-native-state-12345"}}
	begin := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+query.Encode(), nil)
	begin.AddCookie(cookie)
	page := httptest.NewRecorder()
	s.Handler().ServeHTTP(page, begin)
	if page.Code != http.StatusOK {
		t.Fatalf("native authorization page: %d %s", page.Code, page.Body.String())
	}
	flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	formCSRF := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(flow) != 2 || len(formCSRF) != 2 {
		t.Fatal("native consent form missing flow or csrf")
	}
	form := url.Values{"flow": {flow[1]}, "csrf": {formCSRF[1]}}
	confirm := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	confirm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	confirm.Header.Set("Origin", origin)
	confirm.AddCookie(cookie)
	callback := httptest.NewRecorder()
	s.Handler().ServeHTTP(callback, confirm)
	if callback.Code != http.StatusFound {
		t.Fatalf("native consent submission: %d %s", callback.Code, callback.Body.String())
	}
	redirect, err := url.Parse(callback.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	code := redirect.Query().Get("code")
	tokenForm := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {"clipp-relay://oauth/callback"}, "code": {code}, "code_verifier": {verifier}}
	token := httptest.NewRecorder()
	tokenRequest := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(tokenForm.Encode()))
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.Handler().ServeHTTP(token, tokenRequest)
	if token.Code != http.StatusOK {
		t.Fatalf("native token exchange: %d %s", token.Code, token.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(token.Body.Bytes(), &response); err != nil || response.AccessToken == "" {
		t.Fatalf("native access token response: %v", err)
	}
	return response.AccessToken, cookie
}

func TestAmbiguousAccountCommitInvalidatesBeforeResponse(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	target, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(context.Background(), `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at) VALUES($1,'https://accounts.google.com',$2,'target@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp())`, target, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.audit_events WHERE account_id=$1; DELETE FROM public.accounts WHERE id=$1`, target)
	})
	var changed AccountChange
	s.SetAccountChangedForTest(func(change AccountChange) string { changed = change; return "" })
	s.commitAccountAccess = func(ctx context.Context, tx persistence.AccountAccessTransaction) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("lost commit acknowledgment")
	}
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"routine_administration"}, "action": {"approve"}, "revision": {"1"}, "plan_id": {"6dd09395-51a0-451c-96b3-716e6038e870"}}
	out := internalRequest(s, "POST", "/admin/accounts/"+target, cookie, form, origin)
	if out.Code != 503 || changed.AccountID != target || !changed.CloseAll {
		t.Fatalf("ambiguous commit response=%d invalidated=%+v", out.Code, changed)
	}
	var status string
	if err = db.Pool.QueryRow(context.Background(), `SELECT status FROM public.accounts WHERE id=$1`, target).Scan(&status); err != nil || status != "Active" {
		t.Fatalf("commit status=%q error=%v", status, err)
	}
}

func TestAmbiguousSuspensionFencesAndReportsUncertainty(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	target, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id) VALUES($1,'https://accounts.google.com',$2,'target@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp(),'Active','6dd09395-51a0-451c-96b3-716e6038e870')`, target, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.audit_events WHERE account_id=$1`, target)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.accounts WHERE id=$1`, target)
	})
	var changed AccountChange
	closed := false
	s.SetAccountChangedForTest(func(change AccountChange) string {
		changed = change
		return "account_access_changed"
	})
	s.SetAccountInvalidationCompleteForTest(func(string) error { closed = true; return nil })
	s.commitAccountAccess = func(ctx context.Context, tx persistence.AccountAccessTransaction) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("lost commit acknowledgment")
	}
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"security_response"}, "action": {"suspend"}, "revision": {"1"}}
	out := internalRequest(s, "POST", "/admin/accounts/"+target, cookie, form, origin)
	if out.Code != 503 || !strings.Contains(out.Body.String(), "Result could not be confirmed") || !changed.CloseAll || !closed {
		t.Fatalf("ambiguous security mutation response=%d callback=%+v closed=%t", out.Code, changed, closed)
	}
	var status string
	var generation int64
	var audited bool
	if err := db.Pool.QueryRow(ctx, `SELECT a.status,a.credential_generation,EXISTS(SELECT 1 FROM public.audit_events e WHERE e.account_id=a.id AND e.event='account_suspended') FROM public.accounts a WHERE a.id=$1`, target).Scan(&status, &generation, &audited); err != nil || status != "Suspended" || generation != 1 || !audited {
		t.Fatalf("commit state=%s generation=%d audited=%t err=%v", status, generation, audited, err)
	}
	if _, quarantined := s.uncertainAccounts.Load(target); !quarantined {
		t.Fatal("lost security commit did not quarantine admission")
	}
}

func TestCommittedOwnerRevocationReportsIncompleteAccountInvalidation(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	var accountID string
	if err := db.Pool.QueryRow(context.Background(), `SELECT account_id FROM public.portal_sessions WHERE credential_digest=$1`, s.digest(s.CurrentPepper, "portal-session", cookie.Value)).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	s.SetAccountInvalidatedForTest(func(string) error { return errors.New("notification failed") })
	s.SetAccountInvalidationFallbackForTest(func(string) error { return errors.New("reconciliation failed") })
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}}
	out := internalRequest(s, http.MethodPost, "/auth/revoke", cookie, form, origin)
	if out.Code != http.StatusServiceUnavailable || !strings.Contains(out.Body.String(), "committed") || !strings.Contains(out.Body.String(), "invalidation") {
		t.Fatalf("incomplete invalidation response=%d %s", out.Code, out.Body.String())
	}
	var generation int64
	var audited bool
	if err := db.Pool.QueryRow(context.Background(), `SELECT credential_generation,EXISTS(SELECT 1 FROM public.audit_events e WHERE e.account_id=accounts.id AND e.event='owner_credentials_revoked') FROM public.accounts WHERE id=$1`, accountID).Scan(&generation, &audited); err != nil || generation != 1 || !audited {
		t.Fatalf("committed revocation generation=%d audited=%t err=%v", generation, audited, err)
	}
}

func TestSuspensionWaitsForAccountRowLockAndRevokesRelayCredential(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	target, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id) VALUES($1,'https://accounts.google.com',$2,'target@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp(),'Active','6dd09395-51a0-451c-96b3-716e6038e870')`, target, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.native_access_tokens WHERE grant_id IN(SELECT id FROM public.native_login_grants WHERE account_id=$1)`, target)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.native_refresh_generations WHERE grant_id IN(SELECT id FROM public.native_login_grants WHERE account_id=$1)`, target)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.native_authorization_codes WHERE account_id=$1`, target)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.native_login_grants WHERE account_id=$1`, target)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.portal_sessions WHERE account_id=$1`, target)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.audit_events WHERE account_id=$1`, target)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.accounts WHERE id=$1`, target)
	})
	raw, _ := nativeAccessForAccount(t, s, db, target, origin)
	if _, err := s.AuthenticateRelay(ctx, raw); err != nil {
		t.Fatalf("credential before suspension: %v", err)
	}
	blocker, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	var locked string
	if err := blocker.QueryRow(ctx, `SELECT id FROM public.accounts WHERE id=$1 FOR UPDATE`, target).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"security_response"}, "action": {"suspend"}, "revision": {"1"}}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- internalRequest(s, "POST", "/admin/accounts/"+target, cookie, form, origin) }()
	select {
	case response := <-result:
		t.Fatalf("suspension bypassed held row lock: %d", response.Code)
	case <-time.After(50 * time.Millisecond):
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-result:
		if response.Code != 303 {
			t.Fatalf("suspension response=%d %s", response.Code, response.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("suspension did not finish")
	}
	if _, err := s.AuthenticateRelay(ctx, raw); !errors.Is(err, ErrInvalidAccess) {
		t.Fatalf("old credential survived suspension: %v", err)
	}
	var status string
	var generation int64
	var terminated bool
	if err := db.Pool.QueryRow(ctx, `SELECT a.status,a.credential_generation,g.terminated_at IS NOT NULL FROM public.accounts a JOIN public.native_login_grants g ON g.account_id=a.id WHERE a.id=$1`, target).Scan(&status, &generation, &terminated); err != nil || status != "Suspended" || generation != 1 || !terminated {
		t.Fatalf("status=%s generation=%d terminated=%t err=%v", status, generation, terminated, err)
	}
}

func TestAdminConflictResponseDoesNotHoldAccountProtection(t *testing.T) {
	actorID := "6dd09395-51a0-451c-96b3-716e6038e870"
	target := "6dd09395-51a0-451c-96b3-716e6038e871"
	credential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	pepper := []byte("test pepper with enough entropy")
	state := &adminHTTPTestState{actorID: actorID, targetID: target}
	s := &Server{Origin: "https://portal.example.test", AdminAllowlistFile: filepath.Join(t.TempDir(), "allowlist.json"), Peppers: map[uint64][]byte{1: pepper}, CurrentPepper: 1, persistence: adminHTTPTestWork{state: state}, accountRates: make(map[string]rate)}
	if err := os.WriteFile(s.AdminAllowlistFile, []byte(`{"revision":1,"emails":["admin@example.test"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s.SetAccountChangedForTest(func(AccountChange) string { return "" })
	cookie := &http.Cookie{Name: sessionCookie, Value: credential}
	csrf := s.csrf(s.CurrentPepper, cookie.Value)
	state.csrfHash = s.digest(s.CurrentPepper, "csrf-store", csrf)
	firstForm := url.Values{"csrf": {csrf}, "reason": {"security_response"}, "action": {"suspend"}, "revision": {"2"}}
	first := httptest.NewRequest(http.MethodPost, "/admin/accounts/"+target, strings.NewReader(firstForm.Encode()))
	first.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	first.Header.Set("Origin", s.Origin)
	first.AddCookie(cookie)
	blocked := &blockingResponseWriter{inner: httptest.NewRecorder(), started: make(chan struct{}, 1), release: make(chan struct{})}
	firstDone := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(blocked, first)
		close(firstDone)
	}()
	select {
	case <-blocked.started:
	case <-time.After(3 * time.Second):
		close(blocked.release)
		t.Fatal("stale revision did not reach its conflict response")
	}
	var releaseOnce sync.Once
	releaseResponse := func() { releaseOnce.Do(func() { close(blocked.release) }) }
	defer releaseResponse()

	secondForm := url.Values{"csrf": {csrf}, "reason": {"routine_administration"}, "action": {"override"}, "revision": {"1"}, "sessions_override": {"2"}}
	secondDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		secondDone <- internalRequest(s, http.MethodPost, "/admin/accounts/"+target, cookie, secondForm, s.Origin)
	}()
	select {
	case response := <-secondDone:
		if response.Code != http.StatusSeeOther {
			t.Fatalf("second mutation did not proceed while first response was blocked: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("conflict response held account protection while writing")
	}
	select {
	case <-firstDone:
		t.Fatal("blocked response unexpectedly completed before being released")
	default:
	}
	releaseResponse()
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first request did not finish after response release")
	}
	if blocked.inner.Code != http.StatusConflict {
		t.Fatalf("stale revision response=%d, want %d", blocked.inner.Code, http.StatusConflict)
	}
	if state.mutation.Action != "override" || state.mutation.Security {
		t.Fatalf("second operation was not applied with its own policy: %+v", state.mutation)
	}
}

func TestOwnerProfileUsesOnePolicySnapshotAcrossConcurrentChange(t *testing.T) {
	s, cookie, db, _ := adminFixture(t)
	planID, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(context.Background(), `INSERT INTO public.quota_plans(id,name,weekly_bytes,sessions) VALUES($1,'Profile next',7,2)`, planID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `UPDATE public.accounts SET plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE email='consistency@gmail.com' AND plan_id=$1`, planID)
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.quota_plans WHERE id=$1`, planID)
	})
	entered, release := make(chan struct{}), make(chan struct{})
	s.beforeProfileRead = func() { close(entered); <-release }
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- internalRequest(s, "GET", "/", cookie, nil, "") }()
	<-entered
	_, err = db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Denied',plan_id=$1,revision=revision+1 WHERE email='consistency@gmail.com'`, planID)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	out := <-result
	if out.Code != 200 || !strings.Contains(out.Body.String(), "Denied") || strings.Contains(out.Body.String(), "Your allowance") || strings.Contains(out.Body.String(), "7 bytes") {
		t.Fatalf("mixed profile snapshot: %d %s", out.Code, out.Body.String())
	}
}

func TestPlanCreateLostCommitAckReplaysOneOperation(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	page := internalRequest(s, "GET", "/admin/plans", cookie, nil, "")
	if page.Code != 200 {
		t.Fatalf("admin page %d", page.Code)
	}
	get := func(name string) string {
		m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
		if len(m) != 2 {
			t.Fatalf("missing %s", name)
		}
		return m[1]
	}
	form := url.Values{"csrf": {get("csrf")}, "operation_id": {get("operation_id")}, "operation_proof": {get("operation_proof")}, "reason": {"routine_administration"}, "name": {"Lost ACK plan"}, "weekly_bytes": {"17"}, "sessions": {"2"}}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.audit_events WHERE plan_id=$1; DELETE FROM public.quota_plans WHERE id=$1`, form.Get("operation_id"))
	})
	lost := true
	requestContext, cancelRequest := context.WithCancel(context.Background())
	s.commitPlanAccess = func(ctx context.Context, tx persistence.AccountAccessTransaction) error {
		err := tx.Commit(ctx)
		if err != nil {
			return err
		}
		if lost {
			lost = false
			cancelRequest() // Commit happened, but the request died before its ACK.
			return errors.New("injected lost acknowledgment")
		}
		return nil
	}
	firstRequest := httptest.NewRequest("POST", "/admin/plans", strings.NewReader(form.Encode())).WithContext(requestContext)
	firstRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	firstRequest.Header.Set("Origin", origin)
	firstRequest.AddCookie(cookie)
	first := httptest.NewRecorder()
	s.Handler().ServeHTTP(first, firstRequest)
	if first.Code != http.StatusSeeOther {
		t.Fatalf("ambiguous committed response %d", first.Code)
	}
	second := internalRequest(s, "POST", "/admin/plans", cookie, form, origin)
	if second.Code != http.StatusSeeOther {
		t.Fatalf("idempotent retry %d", second.Code)
	}
	var plans, audits int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.quota_plans WHERE name='Lost ACK plan'`).Scan(&plans); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events WHERE plan_id=$1 AND event='plan_created'`, form.Get("operation_id")).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if plans != 1 || audits != 1 {
		t.Fatalf("duplicate operation plans=%d audits=%d", plans, audits)
	}
	form.Set("weekly_bytes", "18")
	if changed := internalRequest(s, "POST", "/admin/plans", cookie, form, origin); changed.Code != 409 {
		t.Fatalf("changed replay %d", changed.Code)
	}
	newPage := internalRequest(s, "GET", "/admin/plans", cookie, nil, "")
	if newPage.Code != 200 {
		t.Fatalf("new operation page = %d", newPage.Code)
	}
	newField := func(name string) string {
		m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(newPage.Body.String())
		if len(m) != 2 {
			t.Fatalf("missing %s", name)
		}
		return m[1]
	}
	retryForm := url.Values{"csrf": {newField("csrf")}, "operation_id": {newField("operation_id")}, "operation_proof": {newField("operation_proof")}, "reason": {"support_correction"}, "name": {"Retry after unresolved commit"}, "weekly_bytes": {"23"}, "sessions": {"3"}}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.audit_events WHERE plan_id=$1; DELETE FROM public.quota_plans WHERE id=$1`, retryForm.Get("operation_id"))
	})
	s.commitPlanAccess = func(context.Context, persistence.AccountAccessTransaction) error {
		return errors.New("injected unresolved commit")
	}
	unresolved := internalRequest(s, "POST", "/admin/plans", cookie, retryForm, origin)
	if unresolved.Code != 503 || !strings.Contains(unresolved.Body.String(), retryForm.Get("operation_id")) || !strings.Contains(unresolved.Body.String(), retryForm.Get("operation_proof")) || !strings.Contains(unresolved.Body.String(), retryForm.Get("name")) {
		t.Fatalf("retry form lost operation: %d %s", unresolved.Code, unresolved.Body.String())
	}
	s.commitPlanAccess = nil
	if retried := internalRequest(s, "POST", "/admin/plans", cookie, retryForm, origin); retried.Code != 303 {
		t.Fatalf("safe retry = %d", retried.Code)
	}
}
