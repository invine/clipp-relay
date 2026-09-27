package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"github.com/jackc/pgx/v5"
)

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
	s := New(pool, c, m, Provider{})
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
	s.SetAccountChanged(func(change AccountChange) { changed = change })
	s.commitAccount = func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("lost commit acknowledgment")
	}
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"routine_administration"}, "action": {"approve"}, "revision": {"1"}, "plan_id": {"6dd09395-51a0-451c-96b3-716e6038e870"}}
	out := internalRequest(s, "POST", "/admin/accounts/"+target, cookie, form, origin)
	if out.Code != 503 || changed.AccountID != target || !changed.CloseAll || !changed.DiscardCredit {
		t.Fatalf("ambiguous commit response=%d invalidated=%+v", out.Code, changed)
	}
	var status string
	if err = db.Pool.QueryRow(context.Background(), `SELECT status FROM public.accounts WHERE id=$1`, target).Scan(&status); err != nil || status != "Active" {
		t.Fatalf("commit status=%q error=%v", status, err)
	}
}

func TestSuspensionWaitsForAccountRowLockAndRevokesRelayCredential(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	target, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	grant, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := opaque()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id) VALUES($1,'https://accounts.google.com',$2,'target@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp(),'Active','6dd09395-51a0-451c-96b3-716e6038e870')`, target, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.relay_access_tokens WHERE grant_id=$1`, grant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.login_grants WHERE id=$1`, grant)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.audit_events WHERE account_id=$1`, target)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.accounts WHERE id=$1`, target)
	})
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.login_grants(id,account_id,credential_generation,client_type,created_at,last_used_at,idle_expires_at,absolute_expires_at) VALUES($1,$2,0,'electron',clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 days',clock_timestamp()+interval '180 days')`, grant, target)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.relay_access_tokens(credential_digest,pepper_version,grant_id,issued_at,expires_at) VALUES($1,$2,$3,clock_timestamp(),clock_timestamp()+interval '15 minutes')`, s.digest(s.CurrentPepper, "relay-access", raw), s.CurrentPepper, grant)
	if err != nil {
		t.Fatal(err)
	}
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
	if err := db.Pool.QueryRow(ctx, `SELECT a.status,a.credential_generation,g.terminated_at IS NOT NULL FROM public.accounts a JOIN public.login_grants g ON g.account_id=a.id WHERE a.id=$1`, target).Scan(&status, &generation, &terminated); err != nil || status != "Suspended" || generation != 1 || !terminated {
		t.Fatalf("status=%s generation=%d terminated=%t err=%v", status, generation, terminated, err)
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
	page := internalRequest(s, "GET", "/admin", cookie, nil, "")
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
	s.commitPlan = func(ctx context.Context, tx pgx.Tx) error {
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
	newPage := internalRequest(s, "GET", "/admin", cookie, nil, "")
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
	s.commitPlan = func(context.Context, pgx.Tx) error { return errors.New("injected unresolved commit") }
	unresolved := internalRequest(s, "POST", "/admin/plans", cookie, retryForm, origin)
	if unresolved.Code != 503 || !strings.Contains(unresolved.Body.String(), retryForm.Get("operation_id")) || !strings.Contains(unresolved.Body.String(), retryForm.Get("operation_proof")) || !strings.Contains(unresolved.Body.String(), retryForm.Get("name")) {
		t.Fatalf("retry form lost operation: %d %s", unresolved.Code, unresolved.Body.String())
	}
	s.commitPlan = nil
	if retried := internalRequest(s, "POST", "/admin/plans", cookie, retryForm, origin); retried.Code != 303 {
		t.Fatalf("safe retry = %d", retried.Code)
	}
}
