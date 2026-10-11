package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/diagnostic"
	"github.com/jackc/pgx/v5"
)

func actionRows(t *testing.T, output string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if id, hasRequest := row["request_id"]; hasRequest && len(id.(string)) != 32 {
			t.Fatalf("missing diagnostic request ID: %v", row)
		}
		rows = append(rows, row)
	}
	return rows
}

func requireActionRow(t *testing.T, rows []map[string]any, event, outcome, reason string) {
	t.Helper()
	for _, row := range rows {
		if row["event"] == event && row["outcome"] == outcome && row["reason"] == reason {
			return
		}
	}
	t.Fatalf("missing %s/%s/%s in %v", event, outcome, reason, rows)
}

func TestOwnerActionOriginRefusalIsSafe(t *testing.T) {
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s := auth.New(nil, config.Config{PortalOrigin: "https://portal.example.test"}, config.Material{}, auth.Provider{})
	s.SetDiagnostics(logger)
	for _, path := range []string{"/auth/logout", "/auth/revoke"} {
		r := httptest.NewRequest(http.MethodPost, path+"?secret=SECRET_QUERY_CANARY", strings.NewReader("csrf=SECRET_CSRF_CANARY"))
		r.Header.Set("Origin", "https://SECRET_ORIGIN_CANARY.test")
		r.Header.Set("X-Request-Id", "SECRET_HEADER_CANARY")
		out := httptest.NewRecorder()
		s.Handler().ServeHTTP(out, r)
		if out.Code != http.StatusForbidden {
			t.Fatalf("%s status %d", path, out.Code)
		}
	}
	logger.Close()
	if strings.Contains(output.String(), "SECRET_") {
		t.Fatalf("private input in diagnostics: %s", output.String())
	}
	rows := actionRows(t, output.String())
	requireActionRow(t, rows, "portal.logout", "refused", "origin_rejected")
	requireActionRow(t, rows, "portal.revoke", "refused", "origin_rejected")
}

func TestOwnerAndAdminMutationDiagnosticsFollowHTTPCommitProof(t *testing.T) {
	c, m, db := fixture(t)
	provider := newProvider(t)
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s := auth.New(db.Pool, c, m, provider.endpoints())
	s.SetDiagnostics(logger)
	owner := loginAs(t, s, provider, "SECRET_OWNER_SUBJECT_CANARY", "SECRET_OWNER_EMAIL_CANARY@example.test", "")
	csrf := csrfFrom(t, s, owner)
	form := url.Values{"csrf": {csrf}}
	if out := portalRequest(s, http.MethodPost, "/auth/logout", owner, form, "https://wrong-origin.test"); out.Code != http.StatusForbidden {
		t.Fatalf("logout origin: %d", out.Code)
	}
	if out := portalRequest(s, http.MethodPost, "/auth/logout", owner, form, c.PortalOrigin); out.Code != http.StatusSeeOther {
		t.Fatalf("logout: %d", out.Code)
	}
	if out := portalRequest(s, http.MethodPost, "/auth/revoke", owner, form, c.PortalOrigin); out.Code != http.StatusUnauthorized {
		t.Fatalf("revocation after logout: %d", out.Code)
	}
	owner = loginAs(t, s, provider, "SECRET_OWNER_SUBJECT_CANARY", "SECRET_OWNER_EMAIL_CANARY@example.test", "")
	form.Set("csrf", csrfFrom(t, s, owner))
	s.SetAccountCommitHookForTest(func(context.Context, pgx.Tx) error { return errors.New("SECRET_ROLLBACK_CANARY") })
	if out := portalRequest(s, http.MethodPost, "/auth/revoke", owner, form, c.PortalOrigin); out.Code != http.StatusServiceUnavailable {
		t.Fatalf("rollback revoke: %d", out.Code)
	}
	// A new serving process has no conservative in-memory uncertainty fence;
	// the rolled-back database state still permits a fresh login.
	s = auth.New(db.Pool, c, m, provider.endpoints())
	s.SetDiagnostics(logger)
	owner = loginAs(t, s, provider, "SECRET_OWNER_SUBJECT_CANARY", "SECRET_OWNER_EMAIL_CANARY@example.test", "")
	form.Set("csrf", csrfFrom(t, s, owner))
	if out := portalRequest(s, http.MethodPost, "/auth/revoke", owner, form, c.PortalOrigin); out.Code != http.StatusSeeOther {
		t.Fatalf("revoke: %d", out.Code)
	}
	logger.Close()
	if strings.Contains(output.String(), "SECRET_") || strings.Contains(output.String(), owner.Value) || strings.Contains(output.String(), csrf) {
		t.Fatalf("private action material leaked: %s", output.String())
	}
	rows := actionRows(t, output.String())
	for _, item := range [][3]string{{"portal.logout", "refused", "origin_rejected"}, {"portal.logout", "success", "none"}, {"portal.revoke", "refused", "invalid_credentials"}, {"portal.revoke", "uncertain", "commit_unconfirmed"}, {"portal.revoke", "success", "none"}} {
		requireActionRow(t, rows, item[0], item[1], item[2])
	}
}

func TestOwnerDeletionLogsAcceptanceAndProvenCompletion(t *testing.T) {
	c, m, db := fixture(t)
	provider := newProvider(t)
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s := auth.New(db.Pool, c, m, provider.endpoints())
	s.SetDiagnostics(logger)
	zero := strings.Repeat("0", 64)
	j := &deletionJournalFixture{head: auth.JournalHead{RepositoryID: "local-qualified-fixture", CoverageFloor: 0, CoverageHash: zero, Sequence: 0, Hash: zero, Format: 1}, etag: "initial", events: map[string][]byte{}, failBeforeHeadOnce: true}
	s.SetDeletionJournal(j, "local-qualified-fixture", 0, zero)
	owner := loginAs(t, s, provider, "SECRET_DELETION_SUBJECT_CANARY", "SECRET_DELETION_EMAIL_CANARY@example.test", "")
	csrf := csrfFrom(t, s, owner)
	form := url.Values{"csrf": {csrf}}
	if out := portalRequest(s, http.MethodPost, "/auth/delete", owner, form, "https://wrong-origin.test"); out.Code != http.StatusServiceUnavailable {
		t.Fatalf("origin refusal status %d", out.Code)
	}
	if out := portalRequest(s, http.MethodPost, "/auth/delete", owner, url.Values{"csrf": {"SECRET_WRONG_CSRF_CANARY"}}, c.PortalOrigin); out.Code != http.StatusForbidden {
		t.Fatalf("csrf refusal status %d", out.Code)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.portal_sessions SET google_authenticated_at=clock_timestamp()-interval '11 minutes' WHERE account_id=(SELECT id FROM public.accounts WHERE subject=$1)`, "SECRET_DELETION_SUBJECT_CANARY"); err != nil {
		t.Fatal(err)
	}
	if out := portalRequest(s, http.MethodPost, "/auth/delete", owner, form, c.PortalOrigin); out.Code != http.StatusForbidden {
		t.Fatalf("recent authentication refusal status %d", out.Code)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.portal_sessions SET google_authenticated_at=clock_timestamp() WHERE account_id=(SELECT id FROM public.accounts WHERE subject=$1)`, "SECRET_DELETION_SUBJECT_CANARY"); err != nil {
		t.Fatal(err)
	}
	out := portalRequest(s, http.MethodPost, "/auth/delete", owner, form, c.PortalOrigin)
	if out.Code != http.StatusAccepted {
		t.Fatalf("admission status %d", out.Code)
	}
	location := out.Header().Get("Location")
	if status := portalRequest(s, http.MethodGet, location, owner, nil, ""); status.Code != http.StatusAccepted {
		t.Fatalf("pending status %d", status.Code)
	}
	if err := s.ReconcileOneDeletion(context.Background()); err == nil {
		t.Fatal("journal failure was accepted as completed")
	}
	if status := portalRequest(s, http.MethodGet, location, owner, nil, ""); status.Code != http.StatusAccepted {
		t.Fatalf("retry pending status %d", status.Code)
	}
	time.Sleep(2 * time.Second)
	if err := s.ReconcileOneDeletion(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := portalRequest(s, http.MethodGet, location, owner, nil, ""); status.Code != http.StatusOK {
		t.Fatalf("completed status %d", status.Code)
	}
	logger.Close()
	logs := output.String()
	for _, secret := range []string{"SECRET_", owner.Value, csrf, location} {
		if strings.Contains(logs, secret) {
			t.Fatalf("deletion material leaked: %s", logs)
		}
	}
	rows := actionRows(t, logs)
	for _, item := range [][3]string{{"portal.delete", "refused", "origin_rejected"}, {"portal.delete", "refused", "csrf_rejected"}, {"portal.delete", "refused", "recent_auth_required"}, {"portal.delete", "success", "none"}} {
		requireActionRow(t, rows, item[0], item[1], item[2])
	}
	var completion bool
	var started, retry bool
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["event"] == "maintenance.deletion" && row["outcome"] == "success" && row["reason"] == "none" {
			if _, hasRequest := row["request_id"]; hasRequest {
				t.Fatalf("background completion claimed request correlation: %v", row)
			}
			completion = true
		}
		if row["event"] == "maintenance.deletion" && row["outcome"] == "started" && row["reason"] == "none" {
			started = true
		}
		if row["event"] == "maintenance.deletion" && row["outcome"] == "failed" && row["reason"] == "journal_unavailable" {
			retry = true
		}
	}
	if !started || !retry || !completion {
		t.Fatalf("missing started/retry/proven completion: %s", logs)
	}
}
