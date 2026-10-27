package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"clipp-relay/internal/config"
)

func TestProductionOAuthAndRelayBoundariesFailClosedWithoutNativeIssuer(t *testing.T) {
	server := New(Dependencies{}, config.Config{}, config.Material{}, Provider{})
	handler := server.Handler()

	query := url.Values{"response_type": {"code"}, "client_id": {"android"}, "redirect_uri": {"clipp-relay://oauth/callback"}, "code_challenge_method": {"S256"}, "code_challenge": {strings.Repeat("a", 43)}, "state": {"native-state-123456"}}
	authorize := httptest.NewRecorder()
	handler.ServeHTTP(authorize, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+query.Encode(), nil))
	if authorize.Code != http.StatusServiceUnavailable {
		t.Fatalf("authorize without verified native issuer status=%d", authorize.Code)
	}
	token := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader("grant_type=refresh_token&client_id=android&refresh_token=legacy-token"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	handler.ServeHTTP(token, request)
	if token.Code != http.StatusServiceUnavailable {
		t.Fatalf("token without verified native issuer status=%d", token.Code)
	}
	if _, err := server.AuthenticateRelay(context.Background(), "legacy-token"); !errors.Is(err, ErrInvalidAccess) {
		t.Fatalf("legacy Relay Authentication error=%v", err)
	}
}

func TestNativeTokenHTTPRejectsClientAssertionsBeforeFosite(t *testing.T) {
	server := &Server{}
	handler := server.NativeHandlerForTest(bytes.Repeat([]byte{0x29}, 32))
	form := url.Values{
		"grant_type":            {"refresh_token"},
		"client_id":             {"android"},
		"refresh_token":         {"ory_rt_" + strings.Repeat("a", 87)},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {"eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJhbmRyb2lkIn0.signature"},
	}
	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Code != 400 {
		t.Fatalf("HTTP assertion request was not rejected by Clipp's strict boundary: %d %s", out.Code, out.Body.String())
	}
}

func TestNativeOAuthRefusesToServeWithoutGrantInvalidationRecipient(t *testing.T) {
	server := New(Dependencies{}, config.Config{}, config.Material{}, Provider{})
	if err := server.EnableNativeOAuth(bytes.Repeat([]byte{0x31}, 32)); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader("grant_type=refresh_token&client_id=android&refresh_token=invalid"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("token endpoint without a grant invalidation recipient returned %d, want 503", response.Code)
	}
}
