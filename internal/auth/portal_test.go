package auth

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"clipp-relay/internal/config"
)

func TestQuotaFormUnitsAreExact(t *testing.T) {
	for _, tc := range []struct {
		value, unit string
		want        int64
	}{{"1", "GiB", 1 << 30}, {"1.5", "GiB", 1536 << 20}, {"0.5", "KiB", 512}, {"0.0009765625", "KiB", 1}, {"12345", "", 12345}, {"0", "TiB", 0}, {"9223372036854775807", "B", math.MaxInt64}} {
		t.Run(tc.value+tc.unit, func(t *testing.T) {
			got, err := parseQuotaBytes(tc.value, tc.unit)
			if err != nil || got != tc.want {
				t.Fatalf("got %d, %v; want %d", got, err, tc.want)
			}
		})
	}
	for _, tc := range []struct{ value, unit string }{{"-1", "GiB"}, {"1.1", "KiB"}, {"1", "GB"}, {"9223372036854775808", "B"}, {"8388608", "TiB"}, {"1/2", "GiB"}, {"1e9", "B"}, {"", "GiB"}} {
		if _, err := parseQuotaBytes(tc.value, tc.unit); err == nil {
			t.Fatalf("accepted invalid or inexact quota %q %q", tc.value, tc.unit)
		}
	}
}

func TestAdminUnitFormsPersistExactByteAllowances(t *testing.T) {
	s, cookie, db, origin := adminFixture(t)
	workspace := internalRequest(s, "GET", "/admin", cookie, nil, "")
	if workspace.Code != http.StatusOK {
		t.Fatalf("workspace %d: %s", workspace.Code, workspace.Body.String())
	}
	form := url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"routine_administration"}, "name": {"Unit form test"}, "weekly_bytes": {"1.5"}, "weekly_bytes_unit": {"GiB"}, "sessions": {"5"}}
	for _, name := range []string{"operation_id", "operation_proof"} {
		match := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(workspace.Body.String())
		if len(match) != 2 {
			t.Fatalf("missing %s", name)
		}
		form.Set(name, match[1])
	}
	planID := form.Get("operation_id")
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.audit_events WHERE plan_id=$1; DELETE FROM public.quota_plans WHERE id=$1`, planID)
	})
	out := internalRequest(s, "POST", "/admin/plans", cookie, form, origin)
	if out.Code != http.StatusSeeOther {
		t.Fatalf("create plan %d: %s", out.Code, out.Body.String())
	}
	var allowance int64
	if err := db.Pool.QueryRow(context.Background(), `SELECT weekly_bytes FROM public.quota_plans WHERE id=$1`, planID).Scan(&allowance); err != nil || allowance != 1536<<20 {
		t.Fatalf("plan allowance %d, %v", allowance, err)
	}
	var accountID string
	if err := db.Pool.QueryRow(context.Background(), `SELECT account_id FROM public.portal_sessions WHERE credential_digest=$1`, s.digest(s.CurrentPepper, "portal-session", cookie.Value)).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.audit_events WHERE account_id=$1`, accountID)
	})
	form = url.Values{"csrf": {s.csrf(s.CurrentPepper, cookie.Value)}, "reason": {"routine_administration"}, "action": {"override"}, "revision": {"1"}, "weekly_bytes_override": {"0.5"}, "weekly_bytes_override_unit": {"MiB"}}
	out = internalRequest(s, "POST", "/admin/accounts/"+accountID, cookie, form, origin)
	if out.Code != http.StatusSeeOther {
		t.Fatalf("override %d: %s", out.Code, out.Body.String())
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT weekly_bytes_override FROM public.accounts WHERE id=$1`, accountID).Scan(&allowance); err != nil || allowance != 512<<10 {
		t.Fatalf("override allowance %d, %v", allowance, err)
	}
	form.Set("revision", "2")
	form.Set("weekly_bytes_override", "0.1")
	form.Set("weekly_bytes_override_unit", "KiB")
	if out = internalRequest(s, "POST", "/admin/accounts/"+accountID, cookie, form, origin); out.Code != http.StatusBadRequest {
		t.Fatalf("inexact override accepted: %d", out.Code)
	}
	profile := internalRequest(s, "GET", "/", cookie, nil, "")
	if profile.Code != http.StatusOK || !strings.Contains(profile.Body.String(), "512 KiB") || !strings.Contains(profile.Body.String(), origin+"/v1/relay") {
		t.Fatalf("profile %d: %s", profile.Code, profile.Body.String())
	}
}

func TestLoggedOutPortalProvidesStylesAndCanonicalSetupURL(t *testing.T) {
	s := New(nil, config.Config{PortalOrigin: "https://relay.example.test"}, config.Material{}, Provider{})
	handler := s.Handler()
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/", nil))
	if out.Code != http.StatusOK {
		t.Fatalf("portal status %d: %s", out.Code, out.Body.String())
	}
	for _, want := range []string{`href="/assets/portal.css"`, `https://relay.example.test/v1/relay`, "Add this relay to Clipp", "Peers sidebar", "Open discovery endpoint", "Authenticated discovery", "HTTPS discovery URL", "Sign in with Google"} {
		if !strings.Contains(out.Body.String(), want) {
			t.Fatalf("missing portal content %q", want)
		}
	}
	for _, tc := range []struct{ path, contentType string }{{"/assets/portal.css", "text/css"}, {"/assets/portal.js", "text/javascript"}} {
		out = httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if out.Code != http.StatusOK || !strings.HasPrefix(out.Header().Get("Content-Type"), tc.contentType) || out.Body.Len() == 0 {
			t.Fatalf("asset %s: %d %s", tc.path, out.Code, out.Header().Get("Content-Type"))
		}
	}
}
