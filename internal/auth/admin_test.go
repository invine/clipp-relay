package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
)

func loginAs(t *testing.T, s *auth.Server, d *providerDouble, subject, email, hd string) *http.Cookie {
	t.Helper()
	d.mu.Lock()
	d.subject = subject
	d.email = email
	d.hostedDomain = hd
	d.mu.Unlock()
	state, binding := start(t, s, nil)
	result := complete(s, state, binding)
	if result.Code != http.StatusSeeOther {
		t.Fatalf("login: %d %s", result.Code, result.Body.String())
	}
	cookie := sessionFrom(result)
	if cookie == nil {
		t.Fatal("session cookie missing")
	}
	return cookie
}

func portalRequest(s *auth.Server, method, path string, cookie *http.Cookie, form url.Values, origin string) *httptest.ResponseRecorder {
	var body strings.Reader
	if form != nil {
		body = *strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, &body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, req)
	return out
}

func csrfFrom(t *testing.T, s *auth.Server, cookie *http.Cookie) string {
	t.Helper()
	out := portalRequest(s, "GET", "/", cookie, nil, "")
	if out.Code != 200 {
		t.Fatalf("profile %d", out.Code)
	}
	matches := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(out.Body.String())
	if len(matches) != 2 {
		t.Fatal("CSRF missing")
	}
	return matches[1]
}

func addPlanOperation(t *testing.T, form url.Values, body string) {
	t.Helper()
	for _, name := range []string{"operation_id", "operation_proof"} {
		matches := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(body)
		if len(matches) != 2 {
			t.Fatalf("missing %s", name)
		}
		form.Set(name, matches[1])
	}
}

func TestAdministratorApprovalAndImmutablePlanThroughPortal(t *testing.T) {
	c, m, db := fixture(t)
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	write := func(body string) {
		t.Helper()
		if e := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(`{"revision":1,"emails":["admin@gmail.com"]}`)
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	admin := loginAs(t, s, d, "admin-test", "Admin@gmail.com", "")
	csrf := csrfFrom(t, s, admin)
	workspace := portalRequest(s, "GET", "/admin", admin, nil, "")
	if workspace.Code != 200 || !strings.Contains(workspace.Body.String(), "1073741824") || !strings.Contains(workspace.Body.String(), "Applied allowlist revision 1") {
		t.Fatalf("admin workspace: %d %s", workspace.Code, workspace.Body.String())
	}
	var adminID string
	var adminRev int64
	if e := db.Pool.QueryRow(context.Background(), `SELECT id,revision FROM public.accounts WHERE subject='admin-test'`).Scan(&adminID, &adminRev); e != nil {
		t.Fatal(e)
	}
	accountPath := "/admin/accounts/" + adminID
	approve := url.Values{"csrf": {csrf}, "reason": {"routine_administration"}, "action": {"approve"}, "revision": {strconv.FormatInt(adminRev, 10)}}
	if out := portalRequest(s, "POST", accountPath, admin, approve, c.PortalOrigin); out.Code != 400 {
		t.Fatalf("missing plan = %d", out.Code)
	}
	approve.Set("plan_id", "6dd09395-51a0-451c-96b3-716e6038e870")
	invalid := url.Values{"csrf": {"wrong"}, "reason": {"routine_administration"}, "action": {"approve"}, "revision": {strconv.FormatInt(adminRev, 10)}, "plan_id": {"6dd09395-51a0-451c-96b3-716e6038e870"}}
	if out := portalRequest(s, "POST", accountPath, admin, invalid, c.PortalOrigin); out.Code != 403 {
		t.Fatalf("invalid CSRF = %d", out.Code)
	}
	if out := portalRequest(s, "POST", accountPath, admin, approve, "https://evil.example"); out.Code != 403 {
		t.Fatalf("invalid Origin = %d", out.Code)
	}
	if out := portalRequest(s, "POST", accountPath, admin, approve, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("self approval = %d", out.Code)
	}
	if out := portalRequest(s, "POST", accountPath, admin, approve, c.PortalOrigin); out.Code != 409 {
		t.Fatalf("stale revision = %d", out.Code)
	}
	profile := portalRequest(s, "GET", "/", admin, nil, "")
	if !strings.Contains(profile.Body.String(), "1073741824 bytes") || !strings.Contains(profile.Body.String(), "Relay Sessions: 5") {
		t.Fatalf("owner allowance: %s", profile.Body.String())
	}
	create := url.Values{"csrf": {csrf}, "reason": {"routine_administration"}, "name": {"Replacement"}, "weekly_bytes": {"0"}, "sessions": {"0"}}
	addPlanOperation(t, create, workspace.Body.String())
	badCreate := url.Values{"csrf": {csrf}, "reason": {"routine_administration"}, "name": {"Invalid"}, "weekly_bytes": {"-1"}, "sessions": {"5"}}
	if out := portalRequest(s, "POST", "/admin/plans", admin, badCreate, c.PortalOrigin); out.Code != 400 {
		t.Fatalf("negative allowance = %d", out.Code)
	}
	if _, e := db.Pool.Exec(context.Background(), `UPDATE public.portal_sessions SET google_authenticated_at=clock_timestamp()-interval '11 minutes' WHERE account_id=$1`, adminID); e != nil {
		t.Fatal(e)
	}
	if out := portalRequest(s, "POST", "/admin/plans", admin, create, c.PortalOrigin); out.Code != 401 {
		t.Fatalf("stale Google auth = %d", out.Code)
	}
	if _, e := db.Pool.Exec(context.Background(), `UPDATE public.portal_sessions SET google_authenticated_at=clock_timestamp() WHERE account_id=$1`, adminID); e != nil {
		t.Fatal(e)
	}
	if out := portalRequest(s, "POST", "/admin/plans", admin, create, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("create plan = %d", out.Code)
	}
	var planID string
	if e := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.quota_plans WHERE name='Replacement' ORDER BY created_at DESC LIMIT 1`).Scan(&planID); e != nil {
		t.Fatal(e)
	}
	user := loginAs(t, s, d, "user-test", "user@third-party.example", "")
	if out := portalRequest(s, "GET", "/admin", user, nil, ""); out.Code != 403 {
		t.Fatalf("ordinary admin view = %d", out.Code)
	}
	if out := portalRequest(s, "GET", "/admin?limit=101", admin, nil, ""); out.Code != 400 {
		t.Fatalf("unbounded page = %d", out.Code)
	}
	if out := portalRequest(s, "GET", "/admin?limit=1", admin, nil, ""); out.Code != 200 || !strings.Contains(out.Body.String(), "Next accounts") {
		t.Fatalf("bounded account page = %d %s", out.Code, out.Body.String())
	}
	var userID string
	var userRev int64
	if e := db.Pool.QueryRow(context.Background(), `SELECT id,revision FROM public.accounts WHERE subject='user-test'`).Scan(&userID, &userRev); e != nil {
		t.Fatal(e)
	}
	var firstPlanID string
	if e := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.quota_plans ORDER BY id LIMIT 1`).Scan(&firstPlanID); e != nil {
		t.Fatal(e)
	}
	var secondPlanID string
	if e := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.quota_plans ORDER BY id OFFSET 1 LIMIT 1`).Scan(&secondPlanID); e != nil {
		t.Fatal(e)
	}
	selectedPage := portalRequest(s, "GET", "/admin?selected="+userID+"&accounts_after="+adminID+"&plans_after="+firstPlanID+"&limit=1", admin, nil, "")
	if selectedPage.Code != 200 || !strings.Contains(selectedPage.Body.String(), `action="/admin/accounts/`+userID+`"`) || !strings.Contains(selectedPage.Body.String(), `value="`+secondPlanID+`"`) {
		t.Fatalf("selected editor and later plan page: %d %s", selectedPage.Code, selectedPage.Body.String())
	}
	approve.Set("revision", strconv.FormatInt(userRev, 10))
	approve.Set("plan_id", planID)
	if out := portalRequest(s, "POST", "/admin/accounts/"+userID, admin, approve, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("user approval = %d", out.Code)
	}
	var auditActor, auditReason string
	var auditClient *string
	if e := db.Pool.QueryRow(context.Background(), `SELECT actor_email,reason,client_type FROM public.audit_events WHERE account_id=$1 AND event='account_approved'`, userID).Scan(&auditActor, &auditReason, &auditClient); e != nil || auditActor != "admin@gmail.com" || auditReason != "routine_administration" || auditClient != nil {
		t.Fatalf("restricted audit fields = %q %q %v %v", auditActor, auditReason, auditClient, e)
	}
	if _, e := db.Pool.Exec(context.Background(), `UPDATE public.quota_plans SET weekly_bytes=99 WHERE id=$1`, planID); e == nil {
		t.Fatal("assigned allowance changed")
	}
	userProfile := portalRequest(s, "GET", "/", user, nil, "")
	if !strings.Contains(userProfile.Body.String(), "0 bytes") || !strings.Contains(userProfile.Body.String(), "Relay Sessions: 0") || strings.Contains(userProfile.Body.String(), "admin@gmail.com") || strings.Contains(userProfile.Body.String(), "routine_administration") {
		t.Fatalf("private owner profile: %s", userProfile.Body.String())
	}
	archive := url.Values{"csrf": {csrf}, "reason": {"routine_administration"}, "revision": {"1"}}
	if out := portalRequest(s, "POST", "/admin/plans/"+planID+"/archive", admin, archive, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("archive = %d", out.Code)
	}
	if out := portalRequest(s, "GET", "/", user, nil, ""); !strings.Contains(out.Body.String(), "0 bytes") {
		t.Fatalf("archived assignment changed: %s", out.Body.String())
	}
	assign := url.Values{"csrf": {csrf}, "reason": {"support_correction"}, "action": {"assign"}, "revision": {"2"}, "plan_id": {planID}}
	if out := portalRequest(s, "POST", "/admin/accounts/"+userID, admin, assign, c.PortalOrigin); out.Code != 409 {
		t.Fatalf("archived plan reassignment = %d", out.Code)
	}
	assign.Set("plan_id", "6dd09395-51a0-451c-96b3-716e6038e870")
	if out := portalRequest(s, "POST", "/admin/accounts/"+userID, admin, assign, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("explicit replacement = %d", out.Code)
	}
	if out := portalRequest(s, "GET", "/", user, nil, ""); !strings.Contains(out.Body.String(), "1073741824 bytes") {
		t.Fatalf("replacement allowance absent: %s", out.Body.String())
	}
	denied := loginAs(t, s, d, "denied-test", "denied@example.test", "")
	var deniedID string
	if e := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject='denied-test'`).Scan(&deniedID); e != nil {
		t.Fatal(e)
	}
	deny := url.Values{"csrf": {csrf}, "reason": {"policy_enforcement"}, "action": {"deny"}, "revision": {"1"}}
	if out := portalRequest(s, "POST", "/admin/accounts/"+deniedID, admin, deny, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("deny pending = %d", out.Code)
	}
	if out := portalRequest(s, "GET", "/", denied, nil, ""); !strings.Contains(out.Body.String(), "not approved") || strings.Contains(out.Body.String(), "policy_enforcement") || strings.Contains(out.Body.String(), "Admin@gmail.com") {
		t.Fatalf("denied profile exposed policy detail: %s", out.Body.String())
	}
	// A failing audit insert must roll back the plan row in the same transaction.
	migrationConfig, e := config.Load(os.Getenv("CLIPP_TEST_MIGRATION_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	mm, e := migrationConfig.ReadDatabaseMaterial()
	if e != nil {
		t.Fatal(e)
	}
	mp, e := database.NewPool(context.Background(), migrationConfig, mm, true)
	if e != nil {
		t.Fatal(e)
	}
	defer mp.Close()
	if _, e = mp.Exec(context.Background(), `CREATE FUNCTION public.fail_plan_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event='plan_created' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_plan_audit BEFORE INSERT ON public.audit_events FOR EACH ROW EXECUTE FUNCTION public.fail_plan_audit()`); e != nil {
		t.Fatal(e)
	}
	defer func() {
		_, _ = mp.Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_plan_audit ON public.audit_events; DROP FUNCTION IF EXISTS public.fail_plan_audit()`)
	}()
	create.Set("name", "Must Roll Back")
	time.Sleep(2 * time.Second) // The browser sequence stays below the account request rate.
	createPage := portalRequest(s, "GET", "/admin", admin, nil, "")
	if createPage.Code != 200 {
		t.Fatalf("new plan form = %d", createPage.Code)
	}
	addPlanOperation(t, create, createPage.Body.String())
	if out := portalRequest(s, "POST", "/admin/plans", admin, create, c.PortalOrigin); out.Code != 503 {
		t.Fatalf("audit failure = %d", out.Code)
	}
	var rolledBack int
	if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.quota_plans WHERE name='Must Roll Back'`).Scan(&rolledBack); e != nil || rolledBack != 0 {
		t.Fatalf("audit rollback = %d %v", rolledBack, e)
	}
	write(`{"revision":2,"emails":["admin@gmail.com","second@gmail.com"]}`)
	second := loginAs(t, s, d, "second-admin-test", "second@gmail.com", "")
	secondCSRF := csrfFrom(t, s, second)
	_ = loginAs(t, s, d, "race-target-test", "race@example.test", "")
	var raceID string
	if e := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject='race-target-test'`).Scan(&raceID); e != nil {
		t.Fatal(e)
	}
	time.Sleep(2 * time.Second)
	racing := func(cookie *http.Cookie, token string) int {
		form := url.Values{"csrf": {token}, "reason": {"routine_administration"}, "action": {"approve"}, "revision": {"1"}, "plan_id": {"6dd09395-51a0-451c-96b3-716e6038e870"}}
		return portalRequest(s, "POST", "/admin/accounts/"+raceID, cookie, form, c.PortalOrigin).Code
	}
	startRace := make(chan struct{})
	results := make(chan int, 2)
	var wg sync.WaitGroup
	for _, participant := range []struct {
		cookie *http.Cookie
		csrf   string
	}{{admin, csrf}, {second, secondCSRF}} {
		wg.Add(1)
		go func(cookie *http.Cookie, token string) {
			defer wg.Done()
			<-startRace
			results <- racing(cookie, token)
		}(participant.cookie, participant.csrf)
	}
	close(startRace)
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for code := range results {
		if code == 303 {
			wins++
		} else if code == 409 {
			conflicts++
		} else {
			t.Fatalf("concurrent edit status %d", code)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("concurrent approval: wins=%d conflicts=%d", wins, conflicts)
	}
	var raceAudits int
	if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events WHERE account_id=$1 AND event='account_approved'`, raceID).Scan(&raceAudits); e != nil || raceAudits != 1 {
		t.Fatalf("concurrent audit = %d %v", raceAudits, e)
	}
	write(`{"revision":3,"emails":[]}`)
	time.Sleep(2 * time.Second) // Keep this integration sequence within the portal's account rate.
	if out := portalRequest(s, "GET", "/admin", admin, nil, ""); out.Code != 403 || out.Header().Get("X-Admin-Policy-Revision") != "3" {
		t.Fatalf("live removal = %d %s", out.Code, out.Header().Get("X-Admin-Policy-Revision"))
	}
	write(`{"revision":4,"emails":["admin@gmail.com"],"unknown":1}`)
	if out := portalRequest(s, "GET", "/admin", admin, nil, ""); out.Code != 403 || out.Header().Get("X-Admin-Policy-Revision") != "0" {
		t.Fatalf("malformed reload = %d %s", out.Code, out.Header().Get("X-Admin-Policy-Revision"))
	}
}

func TestPlanMutationRechecksGoogleAgeAfterActorLockWait(t *testing.T) {
	c, m, db := fixture(t)
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if e := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["lock-admin@gmail.com"]}`), 0600); e != nil {
		t.Fatal(e)
	}
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	admin := loginAs(t, s, d, "lock-admin-test", "lock-admin@gmail.com", "")
	csrf := csrfFrom(t, s, admin)
	var actorID string
	if e := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject='lock-admin-test'`).Scan(&actorID); e != nil {
		t.Fatal(e)
	}
	blocker, e := db.Pool.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Rollback(context.Background())
	var locked string
	if e := blocker.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE id=$1 FOR UPDATE`, actorID).Scan(&locked); e != nil {
		t.Fatal(e)
	}
	form := url.Values{"csrf": {csrf}, "reason": {"routine_administration"}, "name": {"After Lock Must Fail"}, "weekly_bytes": {"1"}, "sessions": {"1"}}
	planPage := portalRequest(s, "GET", "/admin", admin, nil, "")
	if planPage.Code != 200 {
		t.Fatalf("plan form = %d", planPage.Code)
	}
	addPlanOperation(t, form, planPage.Body.String())
	result := make(chan int, 1)
	go func() { result <- portalRequest(s, "POST", "/admin/plans", admin, form, c.PortalOrigin).Code }()
	waiting := false
	for deadline := time.Now().Add(180 * time.Millisecond); time.Now().Before(deadline); {
		var count int
		if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE usename=current_user AND wait_event_type='Lock' AND query LIKE '%public.accounts WHERE id=$1 FOR UPDATE%'`).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count > 0 {
			waiting = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !waiting {
		t.Fatal("mutation did not reach actor row lock")
	}
	if _, e := db.Pool.Exec(context.Background(), `UPDATE public.portal_sessions SET google_authenticated_at=clock_timestamp()-interval '11 minutes' WHERE account_id=$1`, actorID); e != nil {
		t.Fatal(e)
	}
	if e := blocker.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	if code := <-result; code != 401 {
		t.Fatalf("stale after lock wait = %d", code)
	}
	var plans int
	if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.quota_plans WHERE name='After Lock Must Fail'`).Scan(&plans); e != nil || plans != 0 {
		t.Fatalf("expired mutation persisted: %d %v", plans, e)
	}
}
