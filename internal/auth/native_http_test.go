package auth

import (
	"bytes"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

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
