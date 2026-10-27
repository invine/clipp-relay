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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/persistence/postgres"
	"clipp-relay/internal/quota"
	"clipp-relay/internal/relay"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
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

func TestAdministratorViewDeniesNonAllowlistedOwner(t *testing.T) {
	c, m, db := fixture(t)
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["authorized-admin@gmail.com"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	owner := loginAs(t, s, d, "ordinary-admin-denial", "ordinary-owner@example.test", "")
	response := portalRequest(s, http.MethodGet, "/admin", owner, nil, "")
	if response.Code != http.StatusForbidden || response.Header().Get("X-Admin-Policy-Revision") != "1" {
		t.Fatalf("non-allowlisted owner admin view = %d policy revision %q", response.Code, response.Header().Get("X-Admin-Policy-Revision"))
	}
}

func TestAdministratorAccessActionFormsFollowCanonicalStateAndRecoveryHold(t *testing.T) {
	c, m, db := fixture(t)
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["access-admin@gmail.com"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	admin := loginAs(t, s, d, "access-action-admin", "access-admin@gmail.com", "")
	loginAs(t, s, d, "access-action-owner", "access-target@example.test", "")
	var accountID string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject='access-action-owner'`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}

	states := []struct {
		name    string
		allowed []string
		absent  []string
	}{
		{name: "Pending", allowed: []string{`id="deny-account-access"`, `id="revoke-account-access"`}, absent: []string{`id="suspend-account-access"`, `id="reactivate-account-access"`, `id="return-account-review"`}},
		{name: "Active", allowed: []string{`id="suspend-account-access"`, `id="deny-account-access"`, `id="revoke-account-access"`}, absent: []string{`id="reactivate-account-access"`, `id="return-account-review"`}},
		{name: "Suspended", allowed: []string{`id="reactivate-account-access"`, `id="deny-account-access"`, `id="revoke-account-access"`}, absent: []string{`id="suspend-account-access"`, `id="return-account-review"`}},
		{name: "Denied", allowed: []string{`id="return-account-review"`, `id="revoke-account-access"`}, absent: []string{`id="suspend-account-access"`, `id="reactivate-account-access"`, `id="deny-account-access"`}},
	}
	for _, state := range states {
		if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status=$2,plan_id='6dd09395-51a0-451c-96b3-716e6038e870',recovery_review_hold=false WHERE id=$1`, accountID, state.name); err != nil {
			t.Fatal(err)
		}
		page := portalRequest(s, http.MethodGet, "/admin?selected="+accountID+"&view=detail", admin, nil, "")
		body := page.Body.String()
		if page.Code != http.StatusOK || !strings.Contains(body, `class="badge status-`+state.name+`">`+state.name) || strings.Contains(body, `select name="action"`) {
			t.Fatalf("%s actions did not use canonical state-specific forms: status=%d body=%s", state.name, page.Code, body)
		}
		for _, want := range state.allowed {
			if !strings.Contains(body, want) {
				t.Fatalf("%s view missing %s", state.name, want)
			}
		}
		for _, unwanted := range state.absent {
			if strings.Contains(body, unwanted) {
				t.Fatalf("%s view unexpectedly offered %s", state.name, unwanted)
			}
		}
		for _, formID := range []string{`id="deny-account-access"`, `id="suspend-account-access"`, `id="revoke-account-access"`} {
			if strings.Contains(body, formID) && (!strings.Contains(body, "access-target@example.test") || !strings.Contains(body, "Reason")) {
				t.Fatalf("access-closing form %s omitted target or reason: %s", formID, body)
			}
		}
	}

	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',recovery_review_hold=true WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	held := portalRequest(s, http.MethodGet, "/admin?selected="+accountID+"&view=detail", admin, nil, "")
	heldBody := held.Body.String()
	for _, want := range []string{`class="badge status-Active">Active`, "Recovery Review Hold", `id="review-recovery-hold"`, "canonical status and quota assignments stay unchanged", "access-target@example.test"} {
		if !strings.Contains(heldBody, want) {
			t.Fatalf("held Active account missing separate review explanation %q: %d %s", want, held.Code, heldBody)
		}
	}
	if strings.Contains(heldBody, `select name="action"`) {
		t.Fatal("Recovery Review Hold remained in the generic status action selector")
	}
	var revision int64
	if err := db.Pool.QueryRow(context.Background(), `SELECT revision FROM public.accounts WHERE id=$1`, accountID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	review := url.Values{"csrf": {csrfFrom(t, s, admin)}, "reason": {"security_response"}, "action": {"recovery-review"}, "revision": {strconv.FormatInt(revision, 10)}}
	if response := portalRequest(s, http.MethodPost, "/admin/accounts/"+accountID, admin, review, c.PortalOrigin); response.Code != http.StatusSeeOther {
		t.Fatalf("explicit Recovery Review Hold action = %d %s", response.Code, response.Body.String())
	}
	var status, planID string
	var recoveryHeld bool
	if err := db.Pool.QueryRow(context.Background(), `SELECT status,plan_id,recovery_review_hold FROM public.accounts WHERE id=$1`, accountID).Scan(&status, &planID, &recoveryHeld); err != nil {
		t.Fatal(err)
	}
	if status != "Active" || planID != "6dd09395-51a0-451c-96b3-716e6038e870" || recoveryHeld {
		t.Fatalf("Recovery Review Hold changed canonical status or quota instead of clearing only the hold: status=%s plan=%s held=%v", status, planID, recoveryHeld)
	}
}

func TestAdministratorAccountSearchFiltersAndBackContext(t *testing.T) {
	c, m, db := fixture(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["filter-admin@gmail.com"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	admin := loginAs(t, s, d, "filter-admin-"+suffix, "Filter-Admin@gmail.com", "")
	loginAs(t, s, d, "filter-owner-"+suffix, "filter-owner-"+suffix+"@example.test", "")
	var adminID, ownerID string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, "filter-admin-"+suffix).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, "filter-owner-"+suffix).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	search := "FILTER-OWNER-" + suffix
	filtered := portalRequest(s, http.MethodGet, "/admin?status=Pending&email="+url.QueryEscape(search)+"&limit=1", admin, nil, "")
	if filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), "1 matching Relay Accounts") || !strings.Contains(filtered.Body.String(), "Review") || !strings.Contains(filtered.Body.String(), "filter-owner-"+suffix) {
		t.Fatalf("case-insensitive filtered account read: %d %s", filtered.Code, filtered.Body.String())
	}
	if !strings.Contains(filtered.Body.String(), `<body class="workspace-page">`) || !strings.Contains(filtered.Body.String(), "Account reviews") || !strings.Contains(filtered.Body.String(), `id="quota-plans" href="/admin/plans"`) || strings.Contains(filtered.Body.String(), `class="plans-pane"`) || strings.Contains(filtered.Body.String(), `class="site-footer"`) {
		t.Fatalf("account review page retained the legacy combined plan panel or missed the Workspace shell: %d %s", filtered.Code, filtered.Body.String())
	}
	literal := portalRequest(s, http.MethodGet, "/admin?email=%25&limit=1", admin, nil, "")
	if literal.Code != http.StatusOK || !strings.Contains(literal.Body.String(), "0 matching Relay Accounts") {
		t.Fatalf("percent wildcard must be literal: %d %s", literal.Code, literal.Body.String())
	}
	detailPath := "/admin?selected=" + ownerID + "&view=detail&accounts_after=" + adminID + "&status=Pending&email=" + url.QueryEscape(search) + "&limit=1"
	detail := portalRequest(s, http.MethodGet, detailPath, admin, nil, "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `href="/admin?accounts_after=`+adminID) || !strings.Contains(detail.Body.String(), "status=Pending") || !strings.Contains(detail.Body.String(), "email=FILTER-OWNER-") {
		t.Fatalf("selected review lost validated list context: %d %s", detail.Code, detail.Body.String())
	}
}

func TestAdministratorQuotaPlanLibraryIsSeparateAndBounded(t *testing.T) {
	c, m, db := fixture(t)
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["plans-admin@gmail.com"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	admin := loginAs(t, s, d, "plans-admin-"+strconv.FormatInt(time.Now().UnixNano(), 10), "plans-admin@gmail.com", "")
	first := portalRequest(s, http.MethodGet, "/admin/plans?limit=1", admin, nil, "")
	for _, want := range []string{"Quota plans", "Create a quota plan", "Weekly allowance", "Relay Sessions", "Accounts already using it keep their saved plan and allowance", `href="/admin"`} {
		if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), want) {
			t.Fatalf("separate quota plan library missing %q: %d %s", want, first.Code, first.Body.String())
		}
	}
	if strings.Contains(first.Body.String(), "Review accounts") || strings.Contains(first.Body.String(), "Selected account") {
		t.Fatalf("quota plan library rendered account review controls: %s", first.Body.String())
	}
	second := portalRequest(s, http.MethodGet, "/admin/plans?after=not-a-uuid", admin, nil, "")
	if second.Code != http.StatusBadRequest {
		t.Fatalf("invalid plan cursor = %d, want 400", second.Code)
	}
	planName := "Zero plan acceptance-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	create := url.Values{"name": {planName}, "weekly_bytes": {"0"}, "weekly_bytes_unit": {"B"}, "sessions": {"0"}, "reason": {"routine_administration"}}
	addPlanOperation(t, create, first.Body.String())
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(first.Body.String())
	if len(csrf) != 2 {
		t.Fatal("plan creation form did not include CSRF")
	}
	create.Set("csrf", csrf[1])
	created := portalRequest(s, http.MethodPost, "/admin/plans", admin, create, c.PortalOrigin)
	createdPath := created.Header().Get("Location")
	if created.Code != http.StatusSeeOther || !strings.HasPrefix(createdPath, "/admin/plans?created=") {
		t.Fatalf("plan creation should return to its library: %d %q", created.Code, created.Header().Get("Location"))
	}
	resultPage := portalRequest(s, http.MethodGet, createdPath, admin, nil, "")
	if resultPage.Code != http.StatusOK || !strings.Contains(resultPage.Body.String(), "Plan created") || !strings.Contains(resultPage.Body.String(), planName) || !strings.Contains(resultPage.Body.String(), "0 bytes") {
		t.Fatalf("committed plan result was not directly reachable: %d %s", resultPage.Code, resultPage.Body.String())
	}
	firstPage := portalRequest(s, http.MethodGet, "/admin/plans?limit=1", admin, nil, "")
	next := regexp.MustCompile(`href="/admin/plans\?after=([0-9a-f-]+)&amp;limit=1"`).FindStringSubmatch(firstPage.Body.String())
	if firstPage.Code != http.StatusOK || len(next) != 2 {
		t.Fatalf("bounded plan library has no stable next page: %d %s", firstPage.Code, firstPage.Body.String())
	}
	nextPage := portalRequest(s, http.MethodGet, "/admin/plans?after="+next[1]+"&limit=1", admin, nil, "")
	if nextPage.Code != http.StatusOK || strings.Count(nextPage.Body.String(), `class="quota-plan"`) != 1 {
		t.Fatalf("stable plan cursor did not return one next row: %d %s", nextPage.Code, nextPage.Body.String())
	}
	updated := portalRequest(s, http.MethodGet, "/admin/plans?limit=100", admin, nil, "")
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), planName) || !strings.Contains(updated.Body.String(), "0 bytes") || !strings.Contains(updated.Body.String(), "Relay Sessions") {
		t.Fatalf("created zero-valued plan was not presented accurately: %d %s", updated.Code, updated.Body.String())
	}
	var zeroPlanID, zeroPlanRevision string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id::text,revision::text FROM public.quota_plans WHERE name=$1`, planName).Scan(&zeroPlanID, &zeroPlanRevision); err != nil {
		t.Fatal(err)
	}
	archiveForm := url.Values{"csrf": {csrf[1]}, "revision": {zeroPlanRevision}, "reason": {"routine_administration"}, "created": {zeroPlanID}}
	if response := portalRequest(s, http.MethodPost, "/admin/plans/"+zeroPlanID+"/archive", admin, archiveForm, c.PortalOrigin); response.Code != http.StatusBadRequest {
		t.Fatalf("unreviewed plan archive = %d, want 400", response.Code)
	}
	archiveForm.Set("confirm", "yes")
	archivePath := "/admin/plans?created=" + zeroPlanID
	if response := portalRequest(s, http.MethodPost, "/admin/plans/"+zeroPlanID+"/archive", admin, archiveForm, c.PortalOrigin); response.Code != http.StatusSeeOther || response.Header().Get("Location") != archivePath {
		t.Fatalf("reviewed plan archive = %d %q", response.Code, response.Header().Get("Location"))
	}
	stale := portalRequest(s, http.MethodPost, "/admin/plans/"+zeroPlanID+"/archive", admin, archiveForm, c.PortalOrigin)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), `href="`+archivePath+`"`) {
		t.Fatalf("stale plan archive did not offer safe library reload: %d %s", stale.Code, stale.Body.String())
	}
}

func csrfFrom(t *testing.T, s *auth.Server, cookie *http.Cookie) string {
	t.Helper()
	out := portalRequest(s, "GET", "/account/security", cookie, nil, "")
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

func TestAccountSecurityActionsThroughPortal(t *testing.T) {
	c, m, db := fixture(t)
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["security@gmail.com"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	unauthorized := httptest.NewRecorder()
	s.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/account/security", nil))
	if unauthorized.Code != http.StatusSeeOther || unauthorized.Header().Get("Location") != "/auth/login" {
		t.Fatalf("signed-out security view was not protected: %d %q", unauthorized.Code, unauthorized.Header().Get("Location"))
	}
	admin := loginAs(t, s, d, "security-admin", "security@gmail.com", "")
	adminCSRF := csrfFrom(t, s, admin)
	subject := "security-owner-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	owner := loginAs(t, s, d, subject, "owner@example.test", "")
	ownerCSRF := csrfFrom(t, s, owner)
	securityPage := portalRequest(s, http.MethodGet, "/account/security", owner, nil, "")
	for _, want := range []string{"Access &amp; security", "owner@example.test", "Active Login Grants:", "name=\"confirm\"", "required", "Delete relay account", "name=\"confirm_email\"", `pattern="owner@example\.test"`, "Deleting your account closes relay access", "return here to review and submit", "Physical connection closure may take a short time"} {
		if securityPage.Code != http.StatusOK || !strings.Contains(securityPage.Body.String(), want) {
			t.Fatalf("security view missing %q: %d %s", want, securityPage.Code, securityPage.Body.String())
		}
	}
	if strings.Contains(securityPage.Body.String(), "confirm(") {
		t.Fatal("security review depends on a script confirmation")
	}
	var id string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	change := func(action, revision string, want int) {
		t.Helper()
		form := url.Values{"csrf": {adminCSRF}, "reason": {"security_response"}, "action": {action}, "revision": {revision}}
		if action == "approve" || action == "reactivate" {
			form.Set("plan_id", "6dd09395-51a0-451c-96b3-716e6038e870")
		}
		if out := portalRequest(s, "POST", "/admin/accounts/"+id, admin, form, c.PortalOrigin); out.Code != want {
			t.Fatalf("%s at revision %s: %d, want %d", action, revision, out.Code, want)
		}
	}
	change("approve", "1", 303)
	state, binding := start(t, s, nil)
	change("suspend", "1", 409)
	if out := complete(s, state, binding); out.Code != 303 {
		t.Fatalf("stale revision fenced a valid Google login: %d", out.Code)
	}
	change("suspend", "2", 303)
	if out := portalRequest(s, "GET", "/", owner, nil, ""); out.Code != 200 || strings.Contains(out.Body.String(), "Your relay account") {
		t.Fatalf("blocked owner Portal Session survived: %d", out.Code)
	}
	change("reactivate", "3", 303)
	change("deny", "4", 303)
	change("approve", "5", 409)
	change("review", "5", 303)
	change("approve", "6", 303)
	owner = loginAs(t, s, d, subject, "owner@example.test", "")
	ownerCSRF = csrfFrom(t, s, owner)
	form := url.Values{"csrf": {ownerCSRF}}
	if out := portalRequest(s, "POST", "/auth/revoke", owner, form, c.PortalOrigin); out.Code != http.StatusBadRequest {
		t.Fatalf("unreviewed account-wide revocation: %d", out.Code)
	}
	form.Set("confirm", "yes")
	if out := portalRequest(s, "POST", "/auth/revoke", owner, form, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("owner Sign out everywhere: %d", out.Code)
	} else if !strings.Contains(out.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("owner cookie not cleared: %s", out.Header().Get("Set-Cookie"))
	}
	if out := portalRequest(s, "GET", "/", owner, nil, ""); out.Code != 200 || strings.Contains(out.Body.String(), "Your relay account") {
		t.Fatalf("owner Portal Session survived revocation: %d", out.Code)
	}
	var auditReason, auditActor *string
	if err := db.Pool.QueryRow(context.Background(), `SELECT reason,actor_email FROM public.audit_events WHERE account_id=$1 AND event='owner_credentials_revoked'`, id).Scan(&auditReason, &auditActor); err != nil || auditReason != nil || auditActor != nil {
		t.Fatalf("owner audit retained identity or reason: reason=%v actor=%v err=%v", auditReason, auditActor, err)
	}
	change("revoke", "7", 303)
	var status string
	var revision, generation int64
	if err := db.Pool.QueryRow(context.Background(), `SELECT status,revision,credential_generation FROM public.accounts WHERE id=$1`, id).Scan(&status, &revision, &generation); err != nil || status != "Active" || revision != 8 || generation != 4 {
		t.Fatalf("final account state=%q revision=%d generation=%d err=%v", status, revision, generation, err)
	}
}

func TestLiveQuotaOverridesPreserveRecordedUsageAndCredentials(t *testing.T) {
	c, m, db := fixture(t)
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["quota-admin@gmail.com"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	var changes []auth.AccountChange
	s.SetAccountChangedForTest(func(change auth.AccountChange) string { changes = append(changes, change); return "" })
	admin := loginAs(t, s, d, "quota-admin-"+strconv.FormatInt(time.Now().UnixNano(), 10), "quota-admin@gmail.com", "")
	csrf := csrfFrom(t, s, admin)
	subject := "quota-owner-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	owner := loginAs(t, s, d, subject, "quota-owner@example.test", "")
	var id string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	approve := url.Values{"csrf": {csrf}, "reason": {"routine_administration"}, "action": {"approve"}, "revision": {"1"}, "plan_id": {"6dd09395-51a0-451c-96b3-716e6038e870"}}
	if out := portalRequest(s, "POST", "/admin/accounts/"+id, admin, approve, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("approve: %d", out.Code)
	}
	_, err := db.Pool.Exec(context.Background(), `INSERT INTO public.weekly_relay_usage(account_id,week_start,recorded_bytes,sequence,latest_operation_id,latest_delta_bytes) VALUES($1,date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date,1000,1,'3ec305cd-15c4-4d3d-83ee-e47ec7febf10',1000)`, id)
	if err != nil {
		t.Fatal(err)
	}
	set := func(rev, bytes, sessions string, close bool) {
		t.Helper()
		form := url.Values{"csrf": {csrf}, "reason": {"policy_enforcement"}, "action": {"override"}, "revision": {rev}, "weekly_bytes_override": {bytes}, "sessions_override": {sessions}}
		if out := portalRequest(s, "POST", "/admin/accounts/"+id, admin, form, c.PortalOrigin); out.Code != 303 {
			t.Fatalf("override %s: %d %s", bytes, out.Code, out.Body.String())
		}
		change := changes[len(changes)-1]
		if change.CloseAll != close || change.WeeklyBytes != mustInt64(t, bytes) || change.SessionLimit != mustInt(t, sessions) {
			t.Fatalf("override %s callback: %+v", bytes, change)
		}
	}
	set("2", "12345", "5", false)
	selected := portalRequest(s, "GET", "/admin?selected="+id, admin, nil, "")
	if selected.Code != 200 || !strings.Contains(selected.Body.String(), `id="quota-override"`) || !strings.Contains(selected.Body.String(), `name="weekly_bytes_override" type="number" min="0" step="any" value="12345"`) || !strings.Contains(selected.Body.String(), `<option value="B" selected>bytes</option>`) || !strings.Contains(selected.Body.String(), `name="sessions_override" type="number" min="0" value="5"`) {
		t.Fatalf("selected policy does not show current overrides: %d %s", selected.Code, selected.Body.String())
	}
	onlyBytes := url.Values{"csrf": {csrf}, "reason": {"policy_enforcement"}, "action": {"override"}, "revision": {"3"}, "weekly_bytes_override": {"999"}}
	if out := portalRequest(s, "POST", "/admin/accounts/"+id, admin, onlyBytes, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("one-field override: %d", out.Code)
	}
	if change := changes[len(changes)-1]; change.WeeklyBytes != 999 || change.SessionLimit != 5 || change.CloseAll {
		t.Fatalf("one-field override reset other limit: %+v", change)
	}
	onlySessions := url.Values{"csrf": {csrf}, "reason": {"policy_enforcement"}, "action": {"override"}, "revision": {"4"}, "sessions_override": {"0"}}
	if out := portalRequest(s, "POST", "/admin/accounts/"+id, admin, onlySessions, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("session-only override: %d", out.Code)
	}
	if change := changes[len(changes)-1]; change.WeeklyBytes != 999 || change.SessionLimit != 0 {
		t.Fatalf("session-only override reset byte cap: %+v", change)
	}
	selected = portalRequest(s, "GET", "/admin?selected="+id, admin, nil, "")
	if selected.Code != 200 || !strings.Contains(selected.Body.String(), `name="weekly_bytes_override" type="number" min="0" step="any" value="999"`) || !strings.Contains(selected.Body.String(), `name="sessions_override" type="number" min="0" value="0"`) {
		t.Fatalf("exact arbitrary-byte and explicit-zero values were not retained: %d %s", selected.Code, selected.Body.String())
	}
	profile := portalRequest(s, "GET", "/", owner, nil, "")
	if profile.Code != 200 || !strings.Contains(profile.Body.String(), "Recorded Relay Usage: 1000 bytes") || !strings.Contains(profile.Body.String(), "999 bytes") || !strings.Contains(profile.Body.String(), "Relay Sessions: 0") {
		t.Fatalf("owner quota profile: %d %s", profile.Code, profile.Body.String())
	}
	clearBytes := url.Values{"csrf": {csrf}, "reason": {"support_correction"}, "action": {"override"}, "revision": {"5"}, "weekly_bytes_override": {""}, "weekly_bytes_override_unit": {"B"}}
	if out := portalRequest(s, "POST", "/admin/accounts/"+id, admin, clearBytes, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("clear allowance override: %d %s", out.Code, out.Body.String())
	}
	if change := changes[len(changes)-1]; change.WeeklyBytes != 1<<30 || change.SessionLimit != 0 {
		t.Fatalf("clearing allowance override did not restore the assigned plan: %+v", change)
	}
	selected = portalRequest(s, "GET", "/admin?selected="+id, admin, nil, "")
	if selected.Code != 200 || !strings.Contains(selected.Body.String(), `name="weekly_bytes_override" type="number" min="0" step="any" value=""`) || !strings.Contains(selected.Body.String(), `name="sessions_override" type="number" min="0" value="0"`) {
		t.Fatalf("cleared override and explicit-zero session limit were conflated: %d %s", selected.Code, selected.Body.String())
	}
	var generation, committed int64
	if err := db.Pool.QueryRow(context.Background(), `SELECT a.credential_generation,u.recorded_bytes FROM public.accounts a JOIN public.weekly_relay_usage u ON u.account_id=a.id WHERE a.id=$1`, id).Scan(&generation, &committed); err != nil || generation != 0 || committed != 1000 {
		t.Fatalf("quota edit changed credentials or usage: generation=%d committed=%d err=%v", generation, committed, err)
	}
}

func TestAmbiguousAdminRevocationClosesRealRelaySessionAndReconciles(t *testing.T) {
	c, m, db := fixture(t)
	c.Secrets.AdminAllowlistFile = filepath.Join(t.TempDir(), "allowlist.json")
	if err := os.WriteFile(c.Secrets.AdminAllowlistFile, []byte(`{"revision":1,"emails":["wire-admin@gmail.com"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	d := newProvider(t)
	s := newAuthWithRuntime(db, c, m, d.endpoints())
	if err := s.EnableNativeOAuth(m.NativeSigningKey); err != nil {
		t.Fatal(err)
	}
	accounting := quota.New(postgres.New(db))
	defer accounting.Close()
	dataPlane, err := relay.NewProtected(s, accounting, relay.Options{ListenAddress: "/ip4/127.0.0.1/tcp/0"})
	if err != nil {
		t.Fatal(err)
	}
	defer dataPlane.Close()
	admin := loginAs(t, s, d, "wire-admin-"+strconv.FormatInt(time.Now().UnixNano(), 10), "wire-admin@gmail.com", "")
	csrf := csrfFrom(t, s, admin)
	subject := "wire-owner-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	owner := loginAs(t, s, d, subject, "wire-owner@example.test", "")
	var id string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"csrf": {csrf}, "reason": {"security_response"}, "action": {"approve"}, "revision": {"1"}, "plan_id": {"6dd09395-51a0-451c-96b3-716e6038e870"}}
	if out := portalRequest(s, "POST", "/admin/accounts/"+id, admin, form, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("approve: %d", out.Code)
	}
	access, _ := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, authorizeClient(t, s, owner, "android", c.PublicClients.AndroidRedirect))
	client, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"), libp2p.DisableRelay())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ai := peer.AddrInfo{ID: dataPlane.Host.ID(), Addrs: dataPlane.Host.Addrs()}
	if err := client.Connect(ctx, ai); err != nil {
		t.Fatal(err)
	}
	stream, err := client.NewStream(ctx, dataPlane.Host.ID(), relay.AuthProtocol)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(fmt.Sprintf(`{"accessToken":%q}`, access))
	var header [10]byte
	n := binary.PutUvarint(header[:], uint64(len(body)))
	if _, err := stream.Write(append(header[:n], body...)); err != nil {
		t.Fatal(err)
	}
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	size, err := binary.ReadUvarint(testByteReader{stream})
	if err != nil {
		t.Fatal(err)
	}
	response := make([]byte, size)
	if _, err := io.ReadFull(stream, response); err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if !strings.Contains(string(response), `"ok":true`) || dataPlane.AccountSessions(id) != 1 {
		t.Fatalf("relay authentication=%s sessions=%d", response, dataPlane.AccountSessions(id))
	}
	form.Set("action", "revoke")
	form.Set("revision", "2")
	s.SetAccountCommitHookForTest(func(ctx context.Context, tx persistence.AccountAccessTransaction) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("lost commit acknowledgment")
	})
	start := time.Now()
	if out := portalRequest(s, "POST", "/admin/accounts/"+id, admin, form, c.PortalOrigin); out.Code != 503 || !strings.Contains(out.Body.String(), "Result could not be confirmed") {
		t.Fatalf("uncertain revoke: %d %s", out.Code, out.Body.String())
	}
	if sessions := dataPlane.AccountSessions(id); sessions != 0 || time.Since(start) > 10*time.Second {
		t.Fatalf("revocation left %d sessions after %s", sessions, time.Since(start))
	}
	for len(client.Network().ConnsToPeer(dataPlane.Host.ID())) != 0 && time.Since(start) < time.Second {
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(client.Network().ConnsToPeer(dataPlane.Host.ID())); n != 0 {
		t.Fatalf("physical relay connection survived: %d", n)
	}
	if _, err := s.AuthenticateRelay(ctx, access); err != auth.ErrInvalidAccess {
		t.Fatalf("access token survived revocation: %v", err)
	}
	freshOwner := loginAs(t, s, d, subject, "wire-owner@example.test", "")
	freshAccess, _ := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, authorizeClient(t, s, freshOwner, "android", c.PublicClients.AndroidRedirect))
	if _, err := s.AuthenticateRelay(ctx, freshAccess); err != nil {
		t.Fatalf("post-revocation admission did not reconcile: %v", err)
	}
	if sessions := dataPlane.AccountSessions(id); sessions != 0 {
		t.Fatalf("reconciliation revived %d closed sessions", sessions)
	}
}

type testByteReader struct{ io.Reader }

func (r testByteReader) ReadByte() (byte, error) {
	var one [1]byte
	_, err := io.ReadFull(r.Reader, one[:])
	return one[0], err
}

func mustInt64(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func mustInt(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
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
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	var accountChanges []auth.AccountChange
	s.SetAccountChangedForTest(func(change auth.AccountChange) string { accountChanges = append(accountChanges, change); return "" })
	admin := loginAs(t, s, d, "admin-test", "Admin@gmail.com", "")
	csrf := csrfFrom(t, s, admin)
	workspace := portalRequest(s, "GET", "/admin", admin, nil, "")
	if workspace.Code != 200 || !strings.Contains(workspace.Body.String(), "Applied allowlist revision 1") {
		t.Fatalf("admin workspace: %d %s", workspace.Code, workspace.Body.String())
	}
	plans := portalRequest(s, "GET", "/admin/plans", admin, nil, "")
	if plans.Code != 200 || !strings.Contains(plans.Body.String(), "1073741824") {
		t.Fatalf("quota plan library: %d %s", plans.Code, plans.Body.String())
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
	stale := portalRequest(s, "POST", accountPath+"?accounts_after="+adminID+"&status=Pending&email=THIRD-PARTY&limit=1", admin, approve, c.PortalOrigin)
	if stale.Code != 409 || !strings.Contains(stale.Body.String(), `accounts_after=`+adminID) || !strings.Contains(stale.Body.String(), `status=Pending`) || !strings.Contains(stale.Body.String(), `email=THIRD-PARTY`) {
		t.Fatalf("stale revision did not retain safe list context: %d %s", stale.Code, stale.Body.String())
	}
	profile := portalRequest(s, "GET", "/", admin, nil, "")
	if !strings.Contains(profile.Body.String(), "1 GiB") || !strings.Contains(profile.Body.String(), "Relay Sessions: 5") {
		t.Fatalf("owner allowance: %s", profile.Body.String())
	}
	create := url.Values{"csrf": {csrf}, "reason": {"routine_administration"}, "name": {"Replacement"}, "weekly_bytes": {"0"}, "sessions": {"0"}}
	addPlanOperation(t, create, plans.Body.String())
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
	if out := portalRequest(s, "GET", "/admin/plans", user, nil, ""); out.Code != http.StatusForbidden {
		t.Fatalf("ordinary admin plan library = %d", out.Code)
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
	selectedPage := portalRequest(s, "GET", "/admin?selected="+userID+"&view=detail&accounts_after="+adminID+"&plans_after="+firstPlanID+"&status=Pending&email=THIRD-PARTY&limit=1", admin, nil, "")
	if selectedPage.Code != 200 || !strings.Contains(selectedPage.Body.String(), `action="/admin/accounts/`+userID+`?`) || !strings.Contains(selectedPage.Body.String(), `value="`+secondPlanID+`"`) || !strings.Contains(selectedPage.Body.String(), "← Back to accounts") || !strings.Contains(selectedPage.Body.String(), "Assigned plan:") || !strings.Contains(selectedPage.Body.String(), `href="/admin?accounts_after=`+adminID) || !strings.Contains(selectedPage.Body.String(), "email=THIRD-PARTY") || !strings.Contains(selectedPage.Body.String(), "status=Pending") {
		t.Fatalf("selected editor and later plan page: %d %s", selectedPage.Code, selectedPage.Body.String())
	}
	var selectedPlanBytes int64
	var selectedPlanSessions int
	if err := db.Pool.QueryRow(context.Background(), `SELECT weekly_bytes,sessions FROM public.quota_plans WHERE id=$1`, secondPlanID).Scan(&selectedPlanBytes, &selectedPlanSessions); err != nil {
		t.Fatal(err)
	}
	previewData := fmt.Sprintf(`data-bytes="%d" data-sessions="%d"`, selectedPlanBytes, selectedPlanSessions)
	if body := selectedPage.Body.String(); !strings.Contains(body, `id="approve-account"`) || !strings.Contains(body, `id="approval-plan"`) || !strings.Contains(body, `id="approval-plan-preview"`) || !strings.Contains(body, previewData) || strings.Contains(body, `id="assign-plan"`) || strings.Contains(body, `id="quota-override"`) {
		t.Fatalf("pending approval must have its own plan preview and no active-account mutations: %d %s", selectedPage.Code, body)
	}
	approve.Set("revision", strconv.FormatInt(userRev, 10))
	approve.Set("plan_id", planID)
	if out := portalRequest(s, "POST", "/admin/accounts/"+userID, admin, approve, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("user approval = %d", out.Code)
	}
	activeEditor := portalRequest(s, "GET", "/admin?selected="+userID+"&view=detail", admin, nil, "")
	if body := activeEditor.Body.String(); activeEditor.Code != 200 || !strings.Contains(body, `id="assign-plan"`) || !strings.Contains(body, `id="assignment-plan-preview"`) || !strings.Contains(body, `id="quota-override"`) || strings.Contains(body, `id="approve-account"`) {
		t.Fatalf("active account must use separate assignment and override forms: %d %s", activeEditor.Code, body)
	}
	if len(accountChanges) == 0 || accountChanges[len(accountChanges)-1].AccountID != userID {
		t.Fatal("approval did not invalidate the affected account")
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
	archive := url.Values{"csrf": {csrf}, "reason": {"routine_administration"}, "revision": {"1"}, "confirm": {"yes"}}
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
	if accountChanges[len(accountChanges)-1].AccountID != userID || accountChanges[len(accountChanges)-1].CloseAll || accountChanges[len(accountChanges)-1].WeeklyBytes != 1073741824 || accountChanges[len(accountChanges)-1].SessionLimit != 5 {
		t.Fatal("plan assignment did not invalidate account")
	}
	if out := portalRequest(s, "GET", "/", user, nil, ""); !strings.Contains(out.Body.String(), "1 GiB") {
		t.Fatalf("replacement allowance absent: %s", out.Body.String())
	}
	time.Sleep(2 * time.Second) // Keep the longer portal acceptance below the account request rate.
	denied := loginAs(t, s, d, "denied-test", "denied@example.test", "")
	var deniedID string
	if e := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject='denied-test'`).Scan(&deniedID); e != nil {
		t.Fatal(e)
	}
	deny := url.Values{"csrf": {csrf}, "reason": {"policy_enforcement"}, "action": {"deny"}, "revision": {"1"}}
	if out := portalRequest(s, "POST", "/admin/accounts/"+deniedID, admin, deny, c.PortalOrigin); out.Code != 303 {
		t.Fatalf("deny pending = %d", out.Code)
	}
	if accountChanges[len(accountChanges)-1].AccountID != deniedID || !accountChanges[len(accountChanges)-1].CloseAll {
		t.Fatal("denial did not invalidate account")
	}
	if out := portalRequest(s, "GET", "/", denied, nil, ""); strings.Contains(out.Body.String(), "Your relay account") {
		t.Fatalf("denial retained old Portal Session: %s", out.Body.String())
	}
	denied = loginAs(t, s, d, "denied-test", "denied@example.test", "")
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
	createPage := portalRequest(s, "GET", "/admin/plans", admin, nil, "")
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
	if _, e := db.Pool.Exec(context.Background(), `UPDATE public.quota_plans SET archived_at=COALESCE(archived_at,clock_timestamp())`); e != nil {
		t.Fatal(e)
	}
	archivedEditor := portalRequest(s, "GET", "/admin?selected="+userID+"&view=detail", admin, nil, "")
	if body := archivedEditor.Body.String(); archivedEditor.Code != 200 || !strings.Contains(body, `id="assign-plan"`) || strings.Contains(body, `id="assignment-plan"`) || !strings.Contains(body, "No assignable Relay Quota plan") || !strings.Contains(body, "archived; saved assignment retained") {
		t.Fatalf("archived assignment must remain visible without offering it for reassignment: %d %s", archivedEditor.Code, body)
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
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
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
	planPage := portalRequest(s, "GET", "/admin/plans", admin, nil, "")
	if planPage.Code != 200 {
		t.Fatalf("plan form = %d", planPage.Code)
	}
	addPlanOperation(t, form, planPage.Body.String())
	result := make(chan int, 1)
	go func() { result <- portalRequest(s, "POST", "/admin/plans", admin, form, c.PortalOrigin).Code }()
	waiting := false
	for deadline := time.Now().Add(180 * time.Millisecond); time.Now().Before(deadline); {
		var count int
		if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE usename=current_user AND wait_event_type='Lock' AND query LIKE '%public.accounts WHERE id=$1 OR id=$2 ORDER BY id FOR UPDATE%'`).Scan(&count); e != nil {
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
