package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"clipp-relay/internal/auth"
)

func TestConsentPolicyAllowsRegisteredCallback(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	c.PublicClients.ExtensionRedirect = "https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "consent-policy", "consent-policy@example.test", "")
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='consent-policy'`); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ client, redirect, policy string }{
		{"electron", "http://127.0.0.1:34567/oauth/callback", "default-src 'none'; form-action 'self' http://127.0.0.1:34567/oauth/callback; base-uri 'none'"},
		{"android", "clipp-relay://oauth/callback", "default-src 'none'; form-action 'self' clipp-relay://oauth/callback; base-uri 'none'"},
		{"android", "clipp-relay://oauth/callback;sandbox", "default-src 'none'; form-action 'self' clipp-relay://oauth/callback%3Bsandbox; base-uri 'none'"},
		{"android", "clipp-relay://oauth/callback,sandbox", "default-src 'none'; form-action 'self' clipp-relay://oauth/callback%2Csandbox; base-uri 'none'"},
		{"extension", c.PublicClients.ExtensionRedirect, "default-src 'none'; form-action 'self' https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay; base-uri 'none'"},
	}
	challenge := sha256.Sum256([]byte(strings.Repeat("a", 43)))
	for _, tc := range cases {
		t.Run(tc.client, func(t *testing.T) {
			if tc.client == "android" {
				c.PublicClients.AndroidRedirect = tc.redirect
			}
			consentServer := auth.New(db.Pool, c, m, d.endpoints())
			q := url.Values{"response_type": {"code"}, "client_id": {tc.client}, "redirect_uri": {tc.redirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}, "state": {"consent-policy-client-state"}}
			req := httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil)
			req.AddCookie(cookie)
			out := httptest.NewRecorder()
			consentServer.Handler().ServeHTTP(out, req)
			if out.Code != 200 {
				t.Fatalf("consent status: %d", out.Code)
			}
			if got := out.Header().Get("Content-Security-Policy"); got != tc.policy {
				t.Fatalf("consent policy blocks registered callback or permits extra destinations: %q", got)
			}
		})
	}
}
