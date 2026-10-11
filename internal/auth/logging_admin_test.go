package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"clipp-relay/internal/diagnostic"
	"github.com/jackc/pgx/v5"
)

func assertActionLog(t *testing.T, data string, event, outcome, reason string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
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
	s.commitAccount = func(ctx context.Context, tx pgx.Tx) error {
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
	s.commitPlan = func(context.Context, pgx.Tx) error { return errors.New("SECRET_ROLLBACK_CANARY") }
	if out := internalRequest(s, http.MethodPost, "/admin/plans", cookie, form, origin); out.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfirmed plan status %d", out.Code)
	}
	s.commitPlan = func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("SECRET_LOST_ACK_CANARY")
	}
	if out := internalRequest(s, http.MethodPost, "/admin/plans", cookie, form, origin); out.Code != http.StatusSeeOther {
		t.Fatalf("proven plan status %d", out.Code)
	}
	s.commitPlan = nil
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
