package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/persistence"
)

func assertActionLog(t *testing.T, data string, event, outcome, reason string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["event"] == event && row["outcome"] == outcome && row["reason"] == reason && len(row["request_id"].(string)) == 32 {
			return
		}
	}
	t.Fatalf("missing %s/%s/%s in %s", event, outcome, reason, data)
}

func TestAdministratorAccountGuardRefusalsKeepFixedAction(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s.SetDiagnostics(logger)
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"security_response"}, "revision": {"1"}}
	path := "/admin/accounts/6dd09395-51a0-451c-96b3-716e6038e870"
	for _, action := range []string{"approve", "deny", "assign", "suspend", "reactivate", "revoke", "override"} {
		form.Set("action", action)
		if out := internalRequest(s, http.MethodPost, path, cookie, form, "https://SECRET_ORIGIN_CANARY.test"); out.Code != http.StatusForbidden {
			t.Fatalf("%s origin status %d", action, out.Code)
		}
	}
	form.Set("action", "approve")
	form.Set("csrf", "SECRET_CSRF_CANARY")
	if out := internalRequest(s, http.MethodPost, path, cookie, form, origin); out.Code != http.StatusForbidden {
		t.Fatalf("csrf status %d", out.Code)
	}
	form.Set("csrf", s.csrf(s.CurrentPepper, cookie.Value))
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.portal_sessions SET google_authenticated_at=clock_timestamp()-interval '11 minutes' WHERE credential_digest=$1`, s.digest(s.CurrentPepper, "portal-session", cookie.Value)); err != nil {
		t.Fatal(err)
	}
	if out := internalRequest(s, http.MethodPost, path, cookie, form, origin); out.Code != http.StatusUnauthorized {
		t.Fatalf("recent-auth status %d", out.Code)
	}
	if err := os.WriteFile(s.AdminAllowlistFile, []byte(`{"revision":2,"emails":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if out := internalRequest(s, http.MethodPost, path, cookie, form, origin); out.Code != http.StatusForbidden {
		t.Fatalf("allowlist status %d", out.Code)
	}
	logger.Close()
	logs := output.String()
	for _, event := range []string{"admin.approve", "admin.deny", "admin.assign_plan", "admin.suspend", "admin.reactivate", "admin.revoke_credentials", "admin.override_quota"} {
		assertActionLog(t, logs, event, "refused", "origin_rejected")
	}
	assertActionLog(t, logs, "admin.approve", "refused", "csrf_rejected")
	assertActionLog(t, logs, "admin.approve", "refused", "recent_auth_required")
	assertActionLog(t, logs, "admin.approve", "refused", "not_authorized")
	for _, secret := range []string{"SECRET_", path, cookie.Value, form.Get("csrf")} {
		if strings.Contains(logs, secret) {
			t.Fatalf("private administrator input leaked: %s", logs)
		}
	}
}

func TestAdministratorSessionInvalidationAfterFirstReadIsNotAuthorized(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s.SetDiagnostics(logger)
	s.beforeAdminDetailsRead = func() {
		if _, err := db.Pool.Exec(context.Background(), `DELETE FROM public.portal_sessions WHERE credential_digest=$1`, s.digest(s.CurrentPepper, "portal-session", cookie.Value)); err != nil {
			t.Fatal(err)
		}
	}
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"security_response"}, "action": {"approve"}, "revision": {"1"}}
	if out := internalRequest(s, http.MethodPost, "/admin/accounts/6dd09395-51a0-451c-96b3-716e6038e870", cookie, form, origin); out.Code != http.StatusUnauthorized {
		t.Fatalf("invalidated session status %d", out.Code)
	}
	logger.Close()
	logs := output.String()
	assertActionLog(t, logs, "admin.approve", "refused", "not_authorized")
	if strings.Contains(logs, cookie.Value) || strings.Contains(logs, form.Get("csrf")) {
		t.Fatalf("session material leaked: %s", logs)
	}
}

func TestAdministratorRecheckUsesCatalogReason(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s.SetDiagnostics(logger)
	target, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at) VALUES($1,'https://accounts.google.com',$2,'SECRET_RECHECK_EMAIL_CANARY@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp())`, target, target); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.audit_events WHERE account_id=$1`, target)
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.accounts WHERE id=$1`, target)
	})
	s.beforeAdminRecheck = func() {
		if _, err := db.Pool.Exec(context.Background(), `UPDATE public.portal_sessions SET google_authenticated_at=clock_timestamp()-interval '11 minutes' WHERE credential_digest=$1`, s.digest(s.CurrentPepper, "portal-session", cookie.Value)); err != nil {
			t.Fatal(err)
		}
	}
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"security_response"}, "action": {"approve"}, "revision": {"1"}, "plan_id": {"6dd09395-51a0-451c-96b3-716e6038e870"}}
	if out := internalRequest(s, http.MethodPost, "/admin/accounts/"+target, cookie, form, origin); out.Code != http.StatusUnauthorized {
		t.Fatalf("stale recheck status %d", out.Code)
	}
	logger.Close()
	logs := output.String()
	assertActionLog(t, logs, "admin.approve", "refused", "recent_auth_required")
	if strings.Contains(logs, "SECRET_") || strings.Contains(logs, target) || strings.Contains(logs, cookie.Value) {
		t.Fatalf("administrator material leaked: %s", logs)
	}
	var status string
	var audited bool
	if err := db.Pool.QueryRow(context.Background(), `SELECT a.status, EXISTS(SELECT 1 FROM public.audit_events e WHERE e.account_id=a.id AND e.event='account_approved') FROM public.accounts a WHERE a.id=$1`, target).Scan(&status, &audited); err != nil || status != "Pending" || audited {
		t.Fatalf("guard refusal changed account status=%s audited=%t err=%v", status, audited, err)
	}
}

func TestUncatalogedAdministratorActionDoesNotClaimUnknownRoute(t *testing.T) {
	s, cookie, _, origin := adminFixture(t)
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s.SetDiagnostics(logger)
	target, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"routine_administration"}, "revision": {"1"}, "action": {"review"}}
	if out := internalRequest(s, http.MethodPost, "/admin/accounts/"+target, cookie, form, origin); out.Code != http.StatusNotFound {
		t.Fatalf("missing review target status %d", out.Code)
	}
	form.Set("action", "SECRET_UNKNOWN_ACTION_CANARY")
	if out := internalRequest(s, http.MethodPost, "/admin/accounts/"+target, cookie, form, origin); out.Code != http.StatusBadRequest {
		t.Fatalf("unknown action status %d", out.Code)
	}
	if out := internalRequest(s, http.MethodGet, "/admin/no-such-route/"+target, cookie, nil, origin); out.Code != http.StatusNotFound {
		t.Fatalf("unknown route status %d", out.Code)
	}
	logger.Close()
	logs := output.String()
	assertActionLog(t, logs, "http.request_refused", "refused", "invalid_route")
	assertActionLog(t, logs, "http.request_refused", "refused", "invalid_request")
	var rows int
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		if line != "" {
			rows++
		}
	}
	if rows != 2 || strings.Contains(logs, "SECRET_") || strings.Contains(logs, target) || strings.Contains(logs, cookie.Value) {
		t.Fatalf("handled review/unknown action produced a false or unsafe row: %s", logs)
	}
}

func TestOwnerRevokeConcurrentInvalidationAndSQLFailureHaveDistinctRows(t *testing.T) {
	t.Run("account removed after session", func(t *testing.T) {
		s, cookie, db, origin := adminFixture(t)
		var output bytes.Buffer
		logger := diagnostic.New(&output)
		s.SetDiagnostics(logger)
		var accountID string
		if err := db.Pool.QueryRow(context.Background(), `SELECT account_id FROM public.portal_sessions WHERE credential_digest=$1`, s.digest(s.CurrentPepper, "portal-session", cookie.Value)).Scan(&accountID); err != nil {
			t.Fatal(err)
		}
		s.beforeOwnerSubjectRead = func() {
			if _, err := db.Pool.Exec(context.Background(), `DELETE FROM public.portal_sessions WHERE account_id=$1`, accountID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(context.Background(), `DELETE FROM public.accounts WHERE id=$1`, accountID); err != nil {
				t.Fatal(err)
			}
		}
		form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}}
		if out := internalRequest(s, http.MethodPost, "/auth/revoke", cookie, form, origin); out.Code != http.StatusServiceUnavailable {
			t.Fatalf("concurrent invalidation status %d", out.Code)
		}
		logger.Close()
		assertActionLog(t, output.String(), "portal.revoke", "refused", "invalid_credentials")
	})
	t.Run("transactional SQL error", func(t *testing.T) {
		s, cookie, _, origin := adminFixture(t)
		var output bytes.Buffer
		logger := diagnostic.New(&output)
		s.SetDiagnostics(logger)
		s.beforeOwnerAccessRecheck = func(ctx context.Context) error {
			return errors.New("injected repository recheck failure")
		}
		form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}}
		if out := internalRequest(s, http.MethodPost, "/auth/revoke", cookie, form, origin); out.Code != http.StatusUnauthorized {
			t.Fatalf("recheck SQL error status %d", out.Code)
		}
		logger.Close()
		assertActionLog(t, output.String(), "portal.revoke", "failed", "database_unavailable")
	})
}

type headOnlyLoggingJournal struct {
	DeletionJournal
	head JournalHead
}

func (j headOnlyLoggingJournal) ReadHead(context.Context) (JournalHead, string, error) {
	return j.head, "initial", nil
}

func TestOwnerDeleteTransactionalSQLFailureKeepsDatabaseReason(t *testing.T) {
	s, cookie, _, origin := adminFixture(t)
	zero := strings.Repeat("0", 64)
	s.SetDeletionJournal(headOnlyLoggingJournal{head: JournalHead{
		RepositoryID: "local-qualified-fixture", CoverageFloor: 0, CoverageHash: zero,
		Sequence: 0, Hash: zero, Format: 1,
	}}, "local-qualified-fixture", 0, zero)
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s.SetDiagnostics(logger)
	s.beforeOwnerDeletionRecheck = func(ctx context.Context) error {
		return errors.New("injected repository recheck failure")
	}
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}}
	if out := internalRequest(s, http.MethodPost, "/auth/delete", cookie, form, origin); out.Code != http.StatusServiceUnavailable {
		t.Fatalf("recheck SQL error status %d", out.Code)
	}
	logger.Close()
	logs := output.String()
	assertActionLog(t, logs, "portal.delete", "failed", "database_unavailable")
	if strings.Contains(logs, cookie.Value) || strings.Contains(logs, "invalid_credentials") {
		t.Fatalf("delete SQL failure claimed credential refusal or leaked credential: %s", logs)
	}
}

func TestAdministratorMissingSecurityTargetKeepsRefusalReason(t *testing.T) {
	s, cookie, _, origin := adminFixture(t)
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s.SetDiagnostics(logger)
	target, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"security_response"}, "revision": {"1"}, "action": {"deny"}}
	if out := internalRequest(s, http.MethodPost, "/admin/accounts/"+target, cookie, form, origin); out.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing target status %d", out.Code)
	}
	logger.Close()
	logs := output.String()
	assertActionLog(t, logs, "admin.deny", "refused", "invalid_request")
	if strings.Contains(logs, target) || strings.Contains(logs, cookie.Value) {
		t.Fatalf("administrator material leaked: %s", logs)
	}
}

func TestOwnerGuardOverloadAndCancellationDoNotClaimDatabaseFailure(t *testing.T) {
	t.Run("overload", func(t *testing.T) {
		s, cookie, _, origin := adminFixture(t)
		var output bytes.Buffer
		logger := diagnostic.New(&output)
		s.SetDiagnostics(logger)
		var releases []func()
		defer func() {
			for _, release := range releases {
				release()
			}
		}()
		s.beforeAccountActionGuard = func() {
			for range 64 {
				op, err := s.persistence.Start(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				releases = append(releases, op.Close)
			}
		}
		form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}}
		if out := internalRequest(s, http.MethodPost, "/auth/revoke", cookie, form, origin); out.Code != http.StatusServiceUnavailable {
			t.Fatalf("overload status %d", out.Code)
		}
		logger.Close()
		assertActionLog(t, output.String(), "http.request_refused", "refused", "overloaded")
		if strings.Contains(output.String(), "database_unavailable") {
			t.Fatalf("overload claimed database failure: %s", output.String())
		}
	})
	t.Run("caller canceled", func(t *testing.T) {
		s, cookie, _, origin := adminFixture(t)
		var output bytes.Buffer
		logger := diagnostic.New(&output)
		s.SetDiagnostics(logger)
		form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}}
		r := httptest.NewRequest(http.MethodPost, "/auth/revoke", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		r.AddCookie(cookie)
		ctx, cancel := context.WithCancel(r.Context())
		r = r.WithContext(ctx)
		s.beforeAccountActionGuard = cancel
		out := httptest.NewRecorder()
		s.Handler().ServeHTTP(out, r)
		if out.Code != http.StatusServiceUnavailable {
			t.Fatalf("canceled status %d", out.Code)
		}
		logger.Close()
		if output.Len() != 0 {
			t.Fatalf("caller cancellation produced action claim: %s", output.String())
		}
	})
}

func TestAdministratorAccountDiagnosticsRespectRevisionAndCommit(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s.SetDiagnostics(logger)
	target, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(context.Background(), `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at) VALUES($1,'https://accounts.google.com',$2,'SECRET_TARGET_EMAIL_CANARY@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp())`, target, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.audit_events WHERE account_id=$1`, target)
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.accounts WHERE id=$1`, target)
	})
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"security_response"}, "action": {"approve"}, "revision": {"1"}, "plan_id": {"6dd09395-51a0-451c-96b3-716e6038e870"}}
	if out := internalRequest(s, http.MethodPost, "/admin/accounts/"+target, cookie, form, "https://SECRET_ORIGIN_CANARY.test"); out.Code != http.StatusForbidden {
		t.Fatalf("origin status %d", out.Code)
	}
	if out := internalRequest(s, http.MethodPost, "/admin/accounts/"+target, cookie, form, origin); out.Code != http.StatusSeeOther {
		t.Fatalf("approval status %d", out.Code)
	}
	if out := internalRequest(s, http.MethodPost, "/admin/accounts/"+target, cookie, form, origin); out.Code != http.StatusConflict {
		t.Fatalf("revision status %d", out.Code)
	}
	form.Set("action", "suspend")
	form.Set("revision", "2")
	s.commitAccountAccess = func(ctx context.Context, tx persistence.AccountAccessTransaction) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("SECRET_LOST_ACK_CANARY")
	}
	if out := internalRequest(s, http.MethodPost, "/admin/accounts/"+target, cookie, form, origin); out.Code != http.StatusServiceUnavailable {
		t.Fatalf("lost acknowledgement status %d", out.Code)
	}
	logger.Close()
	logs := output.String()
	if strings.Contains(logs, "SECRET_") || strings.Contains(logs, target) || strings.Contains(logs, cookie.Value) {
		t.Fatalf("private administrator material leaked: %s", logs)
	}
	assertActionLog(t, logs, "admin.approve", "success", "none")
	assertActionLog(t, logs, "admin.approve", "refused", "revision_conflict")
	assertActionLog(t, logs, "admin.suspend", "uncertain", "commit_unconfirmed")
	var status string
	var audited bool
	if err := db.Pool.QueryRow(context.Background(), `SELECT a.status,EXISTS(SELECT 1 FROM public.audit_events e WHERE e.account_id=a.id AND e.event='account_suspended') FROM public.accounts a WHERE a.id=$1`, target).Scan(&status, &audited); err != nil || status != "Suspended" || !audited {
		t.Fatalf("business commit/audit status=%s audited=%t err=%v", status, audited, err)
	}
}

func TestAdministratorPlanDiagnosticsDistinguishProofFromUncertainty(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s.SetDiagnostics(logger)
	page := internalRequest(s, http.MethodGet, "/admin", cookie, nil, "")
	if page.Code != http.StatusOK {
		t.Fatalf("admin page status %d", page.Code)
	}
	field := func(name string) string {
		m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
		if len(m) != 2 {
			t.Fatalf("missing %s", name)
		}
		return m[1]
	}
	form := url.Values{"csrf": {field("csrf")}, "operation_id": {field("operation_id")}, "operation_proof": {field("operation_proof")}, "reason": {"routine_administration"}, "name": {"SECRET_PLAN_NAME_CANARY"}, "weekly_bytes": {"17"}, "sessions": {"2"}}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.audit_events WHERE plan_id=$1`, form.Get("operation_id"))
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.quota_plans WHERE id=$1`, form.Get("operation_id"))
	})
	if out := internalRequest(s, http.MethodPost, "/admin/plans", cookie, form, "https://SECRET_ORIGIN_CANARY.test"); out.Code != http.StatusForbidden {
		t.Fatalf("plan origin status %d", out.Code)
	}
	wrongCSRF := url.Values{"csrf": {"SECRET_CSRF_CANARY"}, "operation_id": {form.Get("operation_id")}, "operation_proof": {form.Get("operation_proof")}, "reason": {"routine_administration"}, "name": {form.Get("name")}, "weekly_bytes": {"17"}, "sessions": {"2"}}
	if out := internalRequest(s, http.MethodPost, "/admin/plans", cookie, wrongCSRF, origin); out.Code != http.StatusForbidden {
		t.Fatalf("plan CSRF status %d", out.Code)
	}
	s.commitPlanAccess = func(context.Context, persistence.AccountAccessTransaction) error {
		return errors.New("SECRET_ROLLBACK_CANARY")
	}
	if out := internalRequest(s, http.MethodPost, "/admin/plans", cookie, form, origin); out.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfirmed plan status %d", out.Code)
	}
	s.commitPlanAccess = func(ctx context.Context, tx persistence.AccountAccessTransaction) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("SECRET_LOST_ACK_CANARY")
	}
	if out := internalRequest(s, http.MethodPost, "/admin/plans", cookie, form, origin); out.Code != http.StatusSeeOther {
		t.Fatalf("proven plan status %d", out.Code)
	}
	s.commitPlanAccess = nil
	if out := internalRequest(s, http.MethodPost, "/admin/plans", cookie, form, origin); out.Code != http.StatusSeeOther {
		t.Fatalf("idempotent replay status %d", out.Code)
	}
	form.Set("weekly_bytes", "18")
	if out := internalRequest(s, http.MethodPost, "/admin/plans", cookie, form, origin); out.Code != http.StatusConflict {
		t.Fatalf("conflicting replay status %d", out.Code)
	}
	archive := url.Values{"csrf": {field("csrf")}, "reason": {"routine_administration"}, "revision": {"1"}}
	archivePath := "/admin/plans/" + form.Get("operation_id") + "/archive"
	if out := internalRequest(s, http.MethodPost, archivePath, cookie, archive, origin); out.Code != http.StatusSeeOther {
		t.Fatalf("archive status %d", out.Code)
	}
	if out := internalRequest(s, http.MethodPost, archivePath, cookie, archive, origin); out.Code != http.StatusConflict {
		t.Fatalf("stale archive status %d", out.Code)
	}
	logger.Close()
	logs := output.String()
	for _, secret := range []string{"SECRET_", form.Get("operation_id"), form.Get("operation_proof"), cookie.Value} {
		if strings.Contains(logs, secret) {
			t.Fatalf("plan material leaked: %s", logs)
		}
	}
	assertActionLog(t, logs, "admin.plan_create", "uncertain", "commit_unconfirmed")
	assertActionLog(t, logs, "admin.plan_create", "refused", "origin_rejected")
	assertActionLog(t, logs, "admin.plan_create", "refused", "csrf_rejected")
	assertActionLog(t, logs, "admin.plan_create", "success", "none")
	assertActionLog(t, logs, "admin.plan_create", "refused", "revision_conflict")
	assertActionLog(t, logs, "admin.plan_archive", "success", "none")
	assertActionLog(t, logs, "admin.plan_archive", "refused", "revision_conflict")
	var plans, audits int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.quota_plans WHERE id=$1`, form.Get("operation_id")).Scan(&plans); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events WHERE plan_id=$1 AND event='plan_created'`, form.Get("operation_id")).Scan(&audits); err != nil || plans != 1 || audits != 1 {
		t.Fatalf("plan/audit count %d/%d err=%v", plans, audits, err)
	}
}
