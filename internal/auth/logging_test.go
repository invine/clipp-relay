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

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/diagnostic"
	"github.com/jackc/pgx/v5"
)

func TestCredentialRefusalsHaveIndependentSafeRequestDiagnostics(t *testing.T) {
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s := auth.New(nil, config.Config{}, config.Material{}, auth.Provider{})
	s.SetDiagnostics(logger)
	for _, path := range []string{"/auth/callback?state=SECRET_STATE_CANARY&code=SECRET_CODE_CANARY", "/auth/callback?state=SECRET_STATE_CANARY&code=SECRET_CODE_CANARY"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("X-Request-Id", strings.Repeat("a", 32))
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d", w.Code)
		}
	}
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {"SECRET_CLIENT_CANARY"}, "refresh_token": {"SECRET_REFRESH_CANARY"}}
	r := httptest.NewRequest(http.MethodPost, "/oauth/token?SECRET_QUERY_CANARY", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("token status %d", w.Code)
	}
	logger.Close()
	rows := strings.TrimSpace(output.String())
	for _, forbidden := range []string{"SECRET_", strings.Repeat("a", 32)} {
		if strings.Contains(rows, forbidden) {
			t.Fatalf("leaked %s: %s", forbidden, rows)
		}
	}
	var ids []string
	for _, line := range strings.Split(rows, "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["level"] != "INFO" || row["outcome"] != "refused" {
			t.Fatalf("wrong category: %v", row)
		}
		id, _ := row["request_id"].(string)
		if len(id) != 32 {
			t.Fatalf("bad request id: %v", row)
		}
		ids = append(ids, id)
	}
	if len(ids) != 3 || ids[0] == ids[1] || ids[0] == ids[2] || ids[1] == ids[2] {
		t.Fatalf("request ids: %v", ids)
	}
}

func TestPortalAndOAuthDiagnosticsDescribeCommittedCredentials(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	provider := newProvider(t)
	provider.mu.Lock()
	provider.subject = "SECRET_SUBJECT_CANARY"
	provider.email = "SECRET_EMAIL_CANARY@example.test"
	provider.mu.Unlock()
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s := auth.New(db.Pool, c, m, provider.endpoints())
	s.SetDiagnostics(logger)
	session := loginAs(t, s, provider, "SECRET_SUBJECT_CANARY", "SECRET_EMAIL_CANARY@example.test", "")
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='SECRET_SUBJECT_CANARY'`); err != nil {
		t.Fatal(err)
	}
	code := authorizeClient(t, s, session, "android", c.PublicClients.AndroidRedirect)
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code_verifier": {strings.Repeat("a", 43)}, "code": {strings.Repeat("w", 43)}}
	if out := tokenPost(s, form); out.Code != http.StatusBadRequest && out.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", out.Code)
	}
	form.Set("code", code)
	out := tokenPost(s, form)
	if out.Code != http.StatusOK {
		t.Fatalf("exchange: %d %s", out.Code, out.Body.String())
	}
	var tokens struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}
	refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {tokens.Refresh}}
	if out := tokenPost(s, refresh); out.Code != http.StatusOK {
		t.Fatalf("refresh: %d", out.Code)
	}
	if out := tokenPost(s, refresh); out.Code != http.StatusUnauthorized {
		t.Fatalf("replay: %d", out.Code)
	}
	logger.Close()
	rows := strings.TrimSpace(output.String())
	for _, forbidden := range []string{"SECRET_", session.Value, code, tokens.Access, tokens.Refresh, "clipp-relay://oauth/callback"} {
		if strings.Contains(rows, forbidden) {
			t.Fatalf("leaked credential/identity: %s", forbidden)
		}
	}
	events := map[string]bool{}
	ids := map[string]bool{}
	for _, line := range strings.Split(rows, "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		key := row["event"].(string) + "/" + row["outcome"].(string) + "/" + row["reason"].(string)
		events[key] = true
		id, _ := row["request_id"].(string)
		if len(id) != 32 || ids[id] {
			t.Fatalf("missing or reused request ID: %v", row)
		}
		ids[id] = true
		if strings.HasPrefix(row["event"].(string), "oauth.") && row["client_type"] != "android" {
			t.Fatalf("unvalidated client: %v", row)
		}
	}
	for _, key := range []string{"portal.login/started/none", "portal.login/success/none", "oauth.authorize/success/none", "oauth.consent/success/none", "oauth.code_exchange/refused/invalid_credentials", "oauth.code_exchange/success/none", "oauth.refresh/success/none", "oauth.refresh/refused/replay_detected"} {
		if !events[key] {
			t.Fatalf("missing %s in %s", key, rows)
		}
	}
}

func TestProviderFailureAndVerificationDiagnosticsStayPrivate(t *testing.T) {
	c, m, db := fixture(t)
	provider := newProvider(t)
	provider.mu.Lock()
	provider.subject = "SECRET_PROVIDER_SUBJECT"
	provider.email = "SECRET_PROVIDER_EMAIL@example.test"
	provider.invalid = "signature"
	provider.mu.Unlock()
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s := auth.New(db.Pool, c, m, provider.endpoints())
	s.SetDiagnostics(logger)
	state, binding := start(t, s, nil)
	if out := complete(s, state, binding); out.Code != http.StatusUnauthorized {
		t.Fatalf("signature status %d", out.Code)
	}
	provider.mu.Lock()
	provider.invalid = ""
	provider.oversizeBody = true
	provider.mu.Unlock()
	state, binding = start(t, s, nil)
	if out := complete(s, state, binding); out.Code != http.StatusUnauthorized {
		t.Fatalf("provider status %d", out.Code)
	}
	logger.Close()
	rows := strings.TrimSpace(output.String())
	if strings.Contains(rows, "SECRET_") {
		t.Fatalf("provider canary leaked: %s", rows)
	}
	found := map[string]bool{}
	for _, line := range strings.Split(rows, "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		found[row["outcome"].(string)+"/"+row["reason"].(string)+"/"+row["level"].(string)] = true
	}
	for _, key := range []string{"refused/verification_failed/INFO", "failed/provider_unavailable/WARN"} {
		if !found[key] {
			t.Fatalf("missing %s: %s", key, rows)
		}
	}
}

func TestSuccessfulPortalPagesAndAssetsAreQuiet(t *testing.T) {
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s := auth.New(nil, config.Config{PortalOrigin: "https://relay.example.test"}, config.Material{}, auth.Provider{})
	s.SetDiagnostics(logger)
	for _, path := range []string{"/", "/assets/portal.css", "/assets/portal.js"} {
		out := httptest.NewRecorder()
		s.Handler().ServeHTTP(out, httptest.NewRequest(http.MethodGet, path, nil))
		if out.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, out.Code)
		}
	}
	logger.Close()
	if output.Len() != 0 {
		t.Fatalf("page activity: %s", output.String())
	}
}

func TestCodeExchangeLostCommitAcknowledgmentIsUncertain(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	provider := newProvider(t)
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	s := auth.New(db.Pool, c, m, provider.endpoints())
	s.SetDiagnostics(logger)
	session := loginAs(t, s, provider, "diagnostic-uncertain-account", "private@example.test", "")
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='diagnostic-uncertain-account'`); err != nil {
		t.Fatal(err)
	}
	code := authorizeClient(t, s, session, "android", c.PublicClients.AndroidRedirect)
	s.SetCredentialCommitHookForTest(func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return errors.New("SECRET_LOST_ACK_CANARY")
	})
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code_verifier": {strings.Repeat("a", 43)}, "code": {code}}
	if out := tokenPost(s, form); out.Code != http.StatusServiceUnavailable {
		t.Fatalf("lost acknowledgment status %d", out.Code)
	}
	s.SetCredentialCommitHookForTest(nil)
	if out := tokenPost(s, form); out.Code != http.StatusUnauthorized {
		t.Fatalf("committed code was reusable: %d", out.Code)
	}
	logger.Close()
	rows := output.String()
	if strings.Contains(rows, "SECRET_") || strings.Contains(rows, code) {
		t.Fatalf("private material leaked: %s", rows)
	}
	var uncertain bool
	for _, line := range strings.Split(strings.TrimSpace(rows), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["event"] == "oauth.code_exchange" && row["outcome"] == "uncertain" && row["reason"] == "commit_unconfirmed" && row["level"] == "WARN" {
			uncertain = true
		}
	}
	if !uncertain {
		t.Fatalf("missing uncertain commit row: %s", rows)
	}
}
