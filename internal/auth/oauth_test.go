package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"clipp-relay/internal/auth"
)

func TestPublicClientCodeAndRefresh(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	session := loginAs(t, s, d, "oauth-account", "oauth@example.test", "")
	_, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='oauth-account'`)
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 43)
	code := authorizeClient(t, s, session, "android", c.PublicClients.AndroidRedirect)
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {"clipp-relay://oauth/callback"}, "code": {"wrong"}, "code_verifier": {verifier}}
	post := func(f url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(f.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := post(form); w.Code == 200 {
		t.Fatal("wrong code succeeded")
	}
	form.Set("code", code)
	if w := post(form); w.Code != 200 {
		t.Fatalf("exchange: %d %s", w.Code, w.Body.String())
	} else {
		var token struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &token); err != nil || token.Access == "" || token.Refresh == "" {
			t.Fatalf("tokens: %v", err)
		}
		if w := post(form); w.Code == 200 {
			t.Fatal("code replay succeeded")
		}
		refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {token.Refresh}}
		if w := post(refresh); w.Code != 200 {
			t.Fatalf("refresh: %d %s", w.Code, w.Body.String())
		}
		if w := post(refresh); w.Code == 200 {
			t.Fatal("consumed refresh succeeded")
		}
	}
	_ = http.MethodGet
}

func authorizeClient(t *testing.T, s *auth.Server, cookie *http.Cookie, client, redirect string) string {
	t.Helper()
	verifier := strings.Repeat("a", 43)
	digest := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {client}, "redirect_uri": {redirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(digest[:])}, "state": {"independent-client-state"}}
	r := httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("%s consent: %d %s", client, w.Code, w.Body.String())
	}
	flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(flow) != 2 || len(csrf) != 2 {
		t.Fatal("consent form missing")
	}
	form := url.Values{"flow": {flow[1]}, "csrf": {csrf[1]}}
	confirm := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
	confirm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	confirm.Header.Set("Origin", s.Origin)
	confirm.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, confirm)
	if w.Code != 302 {
		t.Fatalf("%s confirm: %d %s", client, w.Code, w.Body.String())
	}
	u, e := url.Parse(w.Header().Get("Location"))
	if e != nil || u.Query().Get("state") != "independent-client-state" || u.Query().Get("code") == "" {
		t.Fatalf("%s callback: %s %v", client, w.Header().Get("Location"), e)
	}
	return u.Query().Get("code")
}
func tokenPost(s *auth.Server, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func redeemClient(t *testing.T, s *auth.Server, client, redirect, code string) (string, string) {
	t.Helper()
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "redirect_uri": {redirect}, "code": {code}, "code_verifier": {strings.Repeat("a", 43)}}
	w := tokenPost(s, form)
	if w.Code != 200 {
		t.Fatalf("%s exchange: %d %s", client, w.Code, w.Body.String())
	}
	var token struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &token); e != nil || token.Access == "" || token.Refresh == "" {
		t.Fatalf("tokens: %v", e)
	}
	return token.Access, token.Refresh
}
func TestAllRegisteredClientsAndBlockedStates(t *testing.T) {
	for _, status := range []string{"Active", "Pending", "Suspended", "Denied"} {
		t.Run(status, func(t *testing.T) {
			c, m, db := fixture(t)
			c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
			c.PublicClients.ExtensionRedirect = "https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"
			d := newProvider(t)
			s := auth.New(db.Pool, c, m, d.endpoints())
			cookie := loginAs(t, s, d, "states-"+status, "states@example.test", "")
			if status != "Pending" {
				_, e := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status=$1,plan_id=CASE WHEN $1='Active' THEN '6dd09395-51a0-451c-96b3-716e6038e870'::uuid ELSE NULL END WHERE subject=$2`, status, "states-"+status)
				if e != nil {
					t.Fatal(e)
				}
			}
			clients := []struct{ client, redirect string }{{"electron", "http://127.0.0.1:34567/oauth/callback"}, {"android", c.PublicClients.AndroidRedirect}, {"extension", c.PublicClients.ExtensionRedirect}}
			for _, entry := range clients {
				if status == "Active" {
					access, _ := redeemClient(t, s, entry.client, entry.redirect, authorizeClient(t, s, cookie, entry.client, entry.redirect))
					if _, _, e := s.AuthenticateAccess(context.Background(), access); e != nil {
						t.Fatalf("%s access: %v", entry.client, e)
					}
				} else {
					verifier := strings.Repeat("a", 43)
					hash := sha256.Sum256([]byte(verifier))
					q := url.Values{"response_type": {"code"}, "client_id": {entry.client}, "redirect_uri": {entry.redirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {"independent-client-state"}}
					r := httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil)
					r.AddCookie(cookie)
					w := httptest.NewRecorder()
					s.Handler().ServeHTTP(w, r)
					u, e := url.Parse(w.Header().Get("Location"))
					if e != nil || u.Query().Get("code") != "" || u.Query().Get("error") != "access_denied" {
						t.Fatalf("%s %s: %d %s", status, entry.client, w.Code, w.Header().Get("Location"))
					}
				}
			}
		})
	}
}
func TestRefreshReplayInvalidatesOnlyOwningGrant(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "replay-account", "replay@example.test", "")
	_, e := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='replay-account'`)
	if e != nil {
		t.Fatal(e)
	}
	accessA, refreshA := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect))
	accessB, _ := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect))
	form := url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refreshA}}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- tokenPost(s, form).Code }()
	}
	wg.Wait()
	close(codes)
	success := 0
	for code := range codes {
		if code == 200 {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent refresh success count %d", success)
	}
	if _, _, e := s.AuthenticateAccess(context.Background(), accessA); e == nil {
		t.Fatal("replay left own access valid")
	}
	if _, _, e := s.AuthenticateAccess(context.Background(), accessB); e != nil {
		t.Fatalf("replay revoked unrelated grant: %v", e)
	}
}
func TestIdentityFenceCancelsUnknownGoogleFlow(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.mu.Lock()
	d.subject = "fenced-unknown-subject"
	d.mu.Unlock()
	s := auth.New(db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	e := s.WithIdentityFence("fenced-unknown-subject", func() error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	w := complete(s, state, binding)
	if w.Code == 303 {
		t.Fatal("pre-fence unknown flow completed")
	}
	var count int
	if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.accounts WHERE subject='fenced-unknown-subject'`).Scan(&count); e != nil || count != 0 {
		t.Fatalf("fenced flow registered account: %d %v", count, e)
	}
	state, binding = start(t, s, nil)
	w = complete(s, state, binding)
	if w.Code != 303 {
		t.Fatalf("new flow blocked: %d", w.Code)
	}
}

func TestIdentityFenceOverflowCancelsUnfinishedFlows(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	for i := 0; i < 4097; i++ {
		subject := fmt.Sprintf("fenced-%04d", i)
		if e := s.WithIdentityFence(subject, func() error { return nil }); e != nil {
			t.Fatal(e)
		}
	}
	if w := complete(s, state, binding); w.Code == 303 {
		t.Fatal("overflow preserved an unfinished flow")
	}
	if w := complete(auth.New(db.Pool, c, m, d.endpoints()), state, binding); w.Code == 303 {
		t.Fatal("restart revived a flow")
	}
	state, binding = start(t, s, nil)
	if w := complete(s, state, binding); w.Code != 303 {
		t.Fatalf("new flow after overflow: %d", w.Code)
	}
}

func TestCodeBindingExpiryAndGeneration(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "binding-account", "binding@example.test", "")
	_, e := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='binding-account'`)
	if e != nil {
		t.Fatal(e)
	}
	code := authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect)
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {code}, "code_verifier": {strings.Repeat("a", 43)}}
	for _, change := range []func(){func() { form.Set("code_verifier", strings.Repeat("b", 43)) }, func() { form.Set("code_verifier", strings.Repeat("a", 43)); form.Set("client_id", "electron") }, func() { form.Set("client_id", "android"); form.Set("redirect_uri", "clipp-relay://other/callback") }} {
		change()
		if w := tokenPost(s, form); w.Code == 200 {
			t.Fatal("wrong code binding issued tokens")
		}
	}
	form.Set("redirect_uri", c.PublicClients.AndroidRedirect)
	if w := tokenPost(s, form); w.Code != 200 {
		t.Fatalf("valid code rejected after wrong attempts: %d", w.Code)
	}
	if w := tokenPost(s, form); w.Code == 200 {
		t.Fatal("code replay succeeded")
	}
	expired := authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect)
	_, e = db.Pool.Exec(context.Background(), `UPDATE public.authorization_codes SET expires_at=clock_timestamp()-interval '1 second' WHERE account_id=(SELECT id FROM public.accounts WHERE subject='binding-account') AND consumed_at IS NULL`)
	if e != nil {
		t.Fatal(e)
	}
	form.Set("code", expired)
	if w := tokenPost(s, form); w.Code == 200 {
		t.Fatal("expired code succeeded")
	}
	changed := authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect)
	_, e = db.Pool.Exec(context.Background(), `UPDATE public.accounts SET credential_generation=credential_generation+1 WHERE subject='binding-account'`)
	if e != nil {
		t.Fatal(e)
	}
	form.Set("code", changed)
	if w := tokenPost(s, form); w.Code == 200 {
		t.Fatal("generation-stale code succeeded")
	}
}
func TestDeletedIdentityCannotCompleteOldGoogleFlow(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.mu.Lock()
	d.subject = "deletion-fence-subject"
	d.mu.Unlock()
	s := auth.New(db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "deletion-fence-subject", "delete@example.test", "")
	_ = cookie
	state, binding := start(t, s, nil)
	e := s.WithIdentityFence("deletion-fence-subject", func() error {
		_, err := db.Pool.Exec(context.Background(), `DELETE FROM public.portal_sessions WHERE account_id=(SELECT id FROM public.accounts WHERE subject='deletion-fence-subject'); DELETE FROM public.accounts WHERE subject='deletion-fence-subject'`)
		return err
	})
	if e != nil {
		t.Fatal(e)
	}
	if w := complete(s, state, binding); w.Code == 303 {
		t.Fatal("old flow re-registered deleted identity")
	}
	state, binding = start(t, s, nil)
	if w := complete(s, state, binding); w.Code != 303 {
		t.Fatalf("fresh flow after deletion: %d", w.Code)
	}
}

func TestUnknownClientBrowserFlowRequiresConsent(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	// The account already has approval, but this browser has no Portal Session.
	cookie := loginAs(t, s, d, "browser-oauth-subject", "browser@example.test", "")
	_, e := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='browser-oauth-subject'`)
	if e != nil {
		t.Fatal(e)
	}
	_ = cookie
	verifier := strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "state": {"independent-client-state"}}
	r := httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), d.server.URL+"/authorize?") {
		t.Fatalf("Google start: %d %s", w.Code, w.Header().Get("Location"))
	}
	google, _ := url.Parse(w.Header().Get("Location"))
	var binding *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-clipp-binding" {
			binding = c
		}
	}
	if binding == nil {
		t.Fatal("browser binding missing")
	}
	callback := httptest.NewRequest("GET", "/auth/callback?state="+url.QueryEscape(google.Query().Get("state"))+"&code="+url.QueryEscape(google.Query().Get("nonce")), nil)
	callback.AddCookie(binding)
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, callback)
	if out.Code != 200 || strings.Contains(out.Body.String(), "SECRET_CANARY") || out.Header().Get("Location") != "" {
		t.Fatalf("consent: %d %s", out.Code, out.Body.String())
	}
	portalCookie := sessionFrom(out)
	if portalCookie == nil {
		t.Fatal("Portal Session missing")
	}
	flow := regexp.MustCompile(`name="flow" value="([^"]+)"`).FindStringSubmatch(out.Body.String())
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(out.Body.String())
	if len(flow) != 2 || len(csrf) != 2 {
		t.Fatal("form missing")
	}
	form := url.Values{"flow": {flow[1]}, "csrf": {csrf[1]}}
	confirm := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		r.AddCookie(portalCookie)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	if w := confirm("https://attacker.example"); w.Code == 302 {
		t.Fatal("cross-origin consent succeeded")
	}
	authorized := confirm(c.PortalOrigin)
	if authorized.Code != 302 {
		t.Fatalf("authorized consent: %d %s", authorized.Code, authorized.Body.String())
	}
	target, e := url.Parse(authorized.Header().Get("Location"))
	if e != nil || target.Query().Get("code") == "" || target.Query().Get("state") != "independent-client-state" || strings.Contains(authorized.Header().Get("Location"), "SECRET_CANARY") {
		t.Fatal("bad app callback")
	}
	if w := confirm(c.PortalOrigin); w.Code == 302 {
		t.Fatal("consent replay succeeded")
	}
	redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, target.Query().Get("code"))
}

func TestGrantCapAndDeadlines(t *testing.T) {
	c, m, db := fixture(t)
	c.PublicClients.AndroidRedirect = "clipp-relay://oauth/callback"
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "cap-account", "cap@example.test", "")
	_, e := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE subject='cap-account'`)
	if e != nil {
		t.Fatal(e)
	}
	code := authorizeClient(t, s, cookie, "android", c.PublicClients.AndroidRedirect)
	_, e = db.Pool.Exec(context.Background(), `INSERT INTO public.login_grants(id,account_id,credential_generation,client_type,created_at,last_used_at,idle_expires_at,absolute_expires_at) SELECT gen_random_uuid(),a.id,a.credential_generation,'android',clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 days',clock_timestamp()+interval '180 days' FROM public.accounts a,generate_series(1,20) WHERE a.subject='cap-account'`)
	if e != nil {
		t.Fatal(e)
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {"android"}, "redirect_uri": {c.PublicClients.AndroidRedirect}, "code": {code}, "code_verifier": {strings.Repeat("a", 43)}}
	if w := tokenPost(s, form); w.Code != 429 {
		t.Fatalf("grant cap: %d", w.Code)
	}
	var audits int
	if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events e JOIN public.accounts a ON a.id=e.account_id WHERE a.subject='cap-account' AND e.event='grant_cap'`).Scan(&audits); e != nil || audits != 1 {
		t.Fatalf("cap audit: %d %v", audits, e)
	}
	_, e = db.Pool.Exec(context.Background(), `UPDATE public.login_grants SET terminated_at=clock_timestamp() WHERE account_id=(SELECT id FROM public.accounts WHERE subject='cap-account')`)
	if e != nil {
		t.Fatal(e)
	}
	access, refresh := redeemClient(t, s, "android", c.PublicClients.AndroidRedirect, code)
	_, e = db.Pool.Exec(context.Background(), `UPDATE public.login_grants SET idle_expires_at=clock_timestamp()-interval '1 second' WHERE account_id=(SELECT id FROM public.accounts WHERE subject='cap-account') AND terminated_at IS NULL`)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.AuthenticateAccess(context.Background(), access); e == nil {
		t.Fatal("idle-expired grant kept access")
	}
	if w := tokenPost(s, url.Values{"grant_type": {"refresh_token"}, "client_id": {"android"}, "refresh_token": {refresh}}); w.Code == 200 {
		t.Fatal("idle-expired grant renewed")
	}
}
