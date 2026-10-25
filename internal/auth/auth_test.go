package auth_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestServer supplies an explicit controlled invalidation adapter for isolated
// Auth tests. Composed HTTP/libp2p acceptance uses relay.NewProtected instead.
func newTestServer(t *testing.T, pool *pgxpool.Pool, c config.Config, m config.Material, provider auth.Provider) *auth.Server {
	t.Helper()
	s := auth.New(pool, c, m, provider)
	s.SetAccountChangedForTest(func(auth.AccountChange) func() { return nil })
	return s
}

func fixture(t *testing.T) (config.Config, config.Material, *database.Runtime) {
	t.Helper()
	path := os.Getenv("CLIPP_TEST_SERVING_CONFIG")
	if path == "" {
		t.Skip("disposable local PostgreSQL fixture not configured")
	}
	c, e := config.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	if c.Database.Host != "localhost" || !strings.HasPrefix(c.Database.Name, "clipp_ticket02_") {
		t.Fatal("fixture must be disposable local PostgreSQL")
	}
	m, e := c.ReadMaterial()
	if e != nil {
		t.Fatal(e)
	}
	pool, e := database.NewPool(context.Background(), c, m, false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	return c, m, database.NewRuntime(pool)
}

type providerDouble struct {
	server                 *httptest.Server
	key                    *rsa.PrivateKey
	rotatedKey             *rsa.PrivateKey
	mu                     sync.Mutex
	nonce                  string
	invalid                string
	calls                  int
	subject                string
	email                  string
	hostedDomain           string
	keyAge                 string
	keyID                  string
	tokenKeyID             string
	audience               any
	keyStatus              int
	oversizeKeyBody        bool
	oversizeKeyHeader      bool
	compressedKeyBody      bool
	issuers                map[string]string
	onKeys                 func()
	tokenCalls             int
	oversizeBody           bool
	compressedOversizeBody bool
	oversizeHeader         bool
	tokenStatus            int
	tokenGate              chan struct{}
	tokenStarted           chan struct{}
}

func newProvider(t *testing.T) *providerDouble {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	d := &providerDouble{key: key, subject: "stable-subject", email: "latest@example.test", keyAge: "60", keyID: "test-key", tokenKeyID: "test-key"}
	d.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/keys":
			d.mu.Lock()
			d.calls++
			keyAge := d.keyAge
			keyID := d.keyID
			keyStatus := d.keyStatus
			oversizeKeyBody := d.oversizeKeyBody
			oversizeKeyHeader := d.oversizeKeyHeader
			compressedKeyBody := d.compressedKeyBody
			key := d.key
			if keyID == "rotated-key" && d.rotatedKey != nil {
				key = d.rotatedKey
			}
			onKeys := d.onKeys
			d.mu.Unlock()
			if keyStatus != 0 && keyStatus != http.StatusOK {
				w.WriteHeader(keyStatus)
				return
			}
			if oversizeKeyHeader {
				w.Header().Set("X-Canary", strings.Repeat("H", 17<<10))
			}
			w.Header().Set("Cache-Control", "public, max-age="+keyAge)
			if oversizeKeyBody {
				if compressedKeyBody {
					w.Header().Set("Content-Encoding", "gzip")
					compressed := gzip.NewWriter(w)
					_, _ = compressed.Write(bytes.Repeat([]byte("B"), (256<<10)+1))
					_ = compressed.Close()
				} else {
					_, _ = w.Write([]byte(strings.Repeat("B", (256<<10)+1)))
				}
			} else {
				encodeKey := func(dst io.Writer) {
					_ = json.NewEncoder(dst).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": keyID, "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB"}}})
				}
				if compressedKeyBody {
					w.Header().Set("Content-Encoding", "gzip")
					compressed := gzip.NewWriter(w)
					encodeKey(compressed)
					_ = compressed.Close()
				} else {
					encodeKey(w)
				}
			}
			if onKeys != nil {
				onKeys()
			}
		case "/token":
			_ = r.ParseForm()
			d.mu.Lock()
			nonce, invalid, subject := r.Form.Get("code"), d.invalid, d.subject
			email, hostedDomain := d.email, d.hostedDomain
			tokenStatus := d.tokenStatus
			d.tokenCalls++
			issuer := d.issuers[nonce]
			gate, started := d.tokenGate, d.tokenStarted
			oversizeBody, oversizeHeader := d.oversizeBody, d.oversizeHeader
			compressedOversizeBody := d.compressedOversizeBody
			d.mu.Unlock()
			if tokenStatus != 0 && tokenStatus != http.StatusOK {
				w.WriteHeader(tokenStatus)
				return
			}
			if oversizeHeader {
				w.Header().Set("X-Canary", strings.Repeat("H", 17<<10))
			}
			if oversizeBody {
				_, _ = w.Write([]byte(strings.Repeat("B", (256<<10)+1)))
				return
			}
			if compressedOversizeBody {
				w.Header().Set("Content-Encoding", "gzip")
				compressed := gzip.NewWriter(w)
				_, _ = compressed.Write(bytes.Repeat([]byte("B"), (256<<10)+1))
				_ = compressed.Close()
				return
			}
			if started != nil {
				started <- struct{}{}
			}
			if gate != nil {
				<-gate
			}
			if issuer == "" {
				issuer = "accounts.google.com"
			}
			claims := map[string]any{"iss": issuer, "sub": subject, "aud": "test-client", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "email": email, "email_verified": true, "name": "SECRET_CANARY_NAME"}
			d.mu.Lock()
			audience := d.audience
			tokenKeyID := d.tokenKeyID
			signingKey := d.key
			if tokenKeyID == "rotated-key" && d.rotatedKey != nil {
				signingKey = d.rotatedKey
			}
			d.mu.Unlock()
			if audience != nil {
				claims["aud"] = audience
			}
			if hostedDomain != "" {
				claims["hd"] = hostedDomain
			}
			switch invalid {
			case "nonce":
				claims["nonce"] = "wrong"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Second).Unix()
			case "expiry-boundary":
				claims["exp"] = time.Now().Unix()
			case "future-iat":
				claims["iat"] = time.Now().Add(2 * time.Minute).Unix()
			case "empty-subject":
				claims["sub"] = ""
			case "long-subject":
				claims["sub"] = strings.Repeat("s", 256)
			case "empty-email":
				claims["email"] = ""
			case "long-email":
				claims["email"] = strings.Repeat("e", 321)
			case "audience":
				claims["aud"] = "other-client"
			case "issuer":
				claims["iss"] = "https://evil.example"
			}
			algorithm := "RS256"
			if invalid == "algorithm" {
				algorithm = "HS256"
			}
			h, _ := json.Marshal(map[string]any{"alg": algorithm, "kid": tokenKeyID})
			p, _ := json.Marshal(claims)
			input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
			digest := sha256.Sum256([]byte(input))
			sig, _ := rsa.SignPKCS1v15(rand.Reader, signingKey, crypto.SHA256, digest[:])
			if invalid == "signature" {
				sig[0] ^= 1
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id_token": input + "." + base64.RawURLEncoding.EncodeToString(sig), "access_token": "SECRET_CANARY_ACCESS"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(d.server.Close)
	return d
}
func (d *providerDouble) endpoints() auth.Provider {
	return auth.Provider{AuthorizationURL: d.server.URL + "/authorize", TokenURL: d.server.URL + "/token", KeysURL: d.server.URL + "/keys"}
}
func start(t *testing.T, s *auth.Server, bind *http.Cookie) (string, *http.Cookie) {
	t.Helper()
	r := httptest.NewRequest("GET", "/auth/login", nil)
	if bind != nil {
		r.AddCookie(bind)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 302 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	u, e := url.Parse(w.Header().Get("Location"))
	if e != nil {
		t.Fatal(e)
	}
	if u.Query().Get("scope") != "openid email" || u.Query().Get("access_type") != "" {
		t.Fatal("wrong Google scopes")
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-clipp-binding" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe binding cookie")
	}
	return u.Query().Get("state") + "|" + u.Query().Get("nonce"), cookie
}
func complete(s *auth.Server, stateNonce string, binding *http.Cookie) *httptest.ResponseRecorder {
	parts := strings.Split(stateNonce, "|")
	r := httptest.NewRequest("GET", "/auth/callback?state="+url.QueryEscape(parts[0])+"&code="+url.QueryEscape(parts[1]), nil)
	if binding != nil {
		r.AddCookie(binding)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func sessionFrom(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-clipp-portal" {
			return c
		}
	}
	return nil
}
func TestGoogleRegistrationAndSessionPersistAcrossRestart(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	first, binding := start(t, s, nil)
	d.mu.Lock()
	d.nonce = strings.Split(first, "|")[1]
	d.mu.Unlock()
	w := complete(s, first, binding)
	if w.Code != 303 {
		t.Fatalf("callback: %d %s", w.Code, w.Body.String())
	}
	session := sessionFrom(w)
	if session == nil || !session.Secure || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode || session.Path != "/" {
		t.Fatal("unsafe portal cookie")
	}
	second, sameBinding := start(t, s, binding)
	if sameBinding.Value != binding.Value {
		t.Fatal("second tab replaced browser binding")
	}
	d.mu.Lock()
	d.nonce = strings.Split(second, "|")[1]
	d.mu.Unlock()
	w2 := complete(s, second, sameBinding)
	if w2.Code != 303 {
		t.Fatalf("second callback: %d", w2.Code)
	}
	var count int
	if e := db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM public.accounts WHERE subject='stable-subject'").Scan(&count); e != nil || count != 1 {
		t.Fatalf("accounts: %d %v", count, e)
	}
	var canaryPresent bool
	if e := db.Pool.QueryRow(context.Background(), `SELECT position('SECRET_CANARY' in row_to_json(a)::text)>0 FROM public.accounts a WHERE subject='stable-subject'`).Scan(&canaryPresent); e != nil || canaryPresent {
		t.Fatalf("provider secret retained: %v %v", canaryPresent, e)
	}
	var audits int
	if e := db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM public.audit_events e JOIN public.accounts a ON a.id=e.account_id WHERE e.event='account_created' AND a.subject='stable-subject'").Scan(&audits); e != nil || audits != 1 {
		t.Fatalf("audit: %d %v", audits, e)
	}
	restarted := newTestServer(t, db.Pool, c, m, d.endpoints())
	home := httptest.NewRequest("GET", "/", nil)
	home.AddCookie(session)
	out := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(out, home)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "Pending") || !strings.Contains(out.Body.String(), "latest@example.test") {
		t.Fatalf("portal after restart: %d %s", out.Code, out.Body.String())
	}
	if strings.Contains(out.Body.String(), "SECRET_CANARY") {
		t.Fatal("provider material rendered")
	}
	csrf := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(out.Body.String())
	if len(csrf) != 2 {
		t.Fatal("csrf missing")
	}
	form := url.Values{"csrf": {csrf[1]}}
	logout := httptest.NewRequest("POST", "/auth/logout", strings.NewReader(form.Encode()))
	logout.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logout.Header.Set("Origin", c.PortalOrigin)
	logout.AddCookie(session)
	result := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(result, logout)
	if result.Code != 303 {
		t.Fatalf("logout: %d", result.Code)
	}
	after := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(after, home)
	if strings.Contains(after.Body.String(), "Pending") {
		t.Fatal("logout left session valid")
	}
	other := httptest.NewRequest("GET", "/", nil)
	other.AddCookie(sessionFrom(w2))
	otherOut := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(otherOut, other)
	if !strings.Contains(otherOut.Body.String(), "Pending") {
		t.Fatal("logout invalidated other session")
	}
}
func TestProviderAndBrowserFailuresDoNotCreateAccount(t *testing.T) {
	for _, kind := range []string{"signature", "algorithm", "nonce", "expired", "expiry-boundary", "future-iat", "empty-subject", "long-subject", "empty-email", "long-email", "audience", "issuer", "state", "binding", "replay", "body", "header", "compressed-body", "jwks-status", "jwks-body", "jwks-header", "jwks-compressed-body"} {
		t.Run(kind, func(t *testing.T) {
			c, m, db := fixture(t)
			d := newProvider(t)
			d.subject = "invalid-" + kind
			s := newTestServer(t, db.Pool, c, m, d.endpoints())
			state, binding := start(t, s, nil)
			d.mu.Lock()
			d.nonce = strings.Split(state, "|")[1]
			d.invalid = kind
			d.oversizeBody = kind == "body"
			d.oversizeHeader = kind == "header"
			d.compressedOversizeBody = kind == "compressed-body"
			if kind == "jwks-status" {
				d.keyStatus = http.StatusNoContent
			}
			d.oversizeKeyBody = kind == "jwks-body" || kind == "jwks-compressed-body"
			d.oversizeKeyHeader = kind == "jwks-header"
			d.compressedKeyBody = kind == "jwks-compressed-body"
			d.mu.Unlock()
			if kind == "binding" {
				binding = &http.Cookie{Name: binding.Name, Value: strings.Repeat("x", 43)}
			}
			if kind == "state" {
				state = strings.Repeat("z", 43) + "|" + strings.Split(state, "|")[1]
			}
			if kind == "replay" {
				ok := complete(s, state, binding)
				if ok.Code != 303 {
					t.Fatalf("initial: %d", ok.Code)
				}
			}
			failed := complete(s, state, binding)
			if failed.Code == 303 {
				t.Fatal("invalid callback accepted")
			}
			if strings.Contains(failed.Body.String(), "stable-subject") || strings.Contains(failed.Body.String(), "latest@example.test") {
				t.Fatal("identity leaked")
			}
			var count int
			if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.accounts WHERE subject=$1`, d.subject).Scan(&count); e != nil || count != map[bool]int{true: 1, false: 0}[kind == "replay"] {
				t.Fatalf("invalid flow created account: %d %v", count, e)
			}
		})
	}
}
func TestOriginAndCSRFRequiredForLogout(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	d.mu.Lock()
	d.nonce = strings.Split(state, "|")[1]
	d.mu.Unlock()
	w := complete(s, state, binding)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	session := sessionFrom(w)
	for _, origin := range []string{"", "https://evil.example"} {
		r := httptest.NewRequest("POST", "/auth/logout", strings.NewReader("csrf=wrong"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		r.AddCookie(session)
		o := httptest.NewRecorder()
		s.Handler().ServeHTTP(o, r)
		if o.Code != 403 {
			t.Fatalf("origin %q: %d", origin, o.Code)
		}
	}
	r := httptest.NewRequest("POST", "/auth/logout", strings.NewReader("csrf=wrong"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", c.PortalOrigin)
	r.AddCookie(session)
	o := httptest.NewRecorder()
	s.Handler().ServeHTTP(o, r)
	if o.Code != 403 {
		t.Fatalf("csrf: %d", o.Code)
	}
}

func TestExpiredSessionsAndDatabaseOutageFailClosed(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	good := complete(s, state, binding)
	if good.Code != 303 {
		t.Fatal(good.Code)
	}
	session := sessionFrom(good)
	if _, e := db.Pool.Exec(context.Background(), `UPDATE public.portal_sessions SET idle_expires_at=clock_timestamp()-interval '1 second' WHERE created_at=(SELECT max(created_at) FROM public.portal_sessions)`); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(session)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "Pending") {
		t.Fatal("idle-expired session accepted")
	}
	state, binding = start(t, s, nil)
	good = complete(s, state, binding)
	if good.Code != 303 {
		t.Fatal(good.Code)
	}
	session = sessionFrom(good)
	if _, e := db.Pool.Exec(context.Background(), `UPDATE public.portal_sessions SET absolute_expires_at=clock_timestamp()-interval '1 second' WHERE created_at=(SELECT max(created_at) FROM public.portal_sessions)`); e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.AddCookie(session)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), "Pending") {
		t.Fatal("absolute-expired session accepted")
	}
	db.Pool.Close()
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/auth/login", nil))
	if w.Code != 503 || sessionFrom(w) != nil {
		t.Fatalf("outage start: %d", w.Code)
	}
}

func TestGlobalRateAndRequestBoundsRejectImmediately(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	r := httptest.NewRequest("GET", "/"+strings.Repeat("a", 8192), nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("long target: %d", w.Code)
	}
	limited := false
	for i := 0; i < 420; i++ {
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if w.Code == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("global rate did not refuse burst")
	}
}

func TestStockRemoteKeySetCachesKeysWithoutApplicationTTL(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.keyAge = "0"
	d.subject = "zero-age-stock-cache"
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	first, binding := start(t, s, nil)
	if w := complete(s, first, binding); w.Code != 303 {
		t.Fatalf("first login: %d", w.Code)
	}
	second, binding := start(t, s, binding)
	if w := complete(s, second, binding); w.Code != 303 {
		t.Fatalf("login using cached key: %d", w.Code)
	}
	d.mu.Lock()
	calls := d.calls
	d.mu.Unlock()
	if calls != 1 {
		t.Fatalf("stock key cache fetched %d times, want one", calls)
	}
}

func TestConcurrentEquivalentLoginsCreateOneAccount(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "concurrent-subject"
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	flows := make([]string, 8)
	bindings := make([]*http.Cookie, 8)
	for i := range flows {
		flows[i], bindings[i] = start(t, s, nil)
	}
	d.issuers = map[string]string{}
	for i, flow := range flows {
		if i%2 == 0 {
			d.issuers[strings.Split(flow, "|")[1]] = "accounts.google.com"
		} else {
			d.issuers[strings.Split(flow, "|")[1]] = "https://accounts.google.com"
		}
	}
	var wg sync.WaitGroup
	errs := make(chan int, len(flows))
	for i := range flows {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := complete(s, flows[i], bindings[i])
			if w.Code != 303 {
				errs <- w.Code
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for status := range errs {
		t.Errorf("concurrent callback: %d", status)
	}
	var count, audits int
	var issuer string
	_ = db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.accounts WHERE subject=$1`, d.subject).Scan(&count)
	_ = db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events a JOIN public.accounts x ON a.account_id=x.id WHERE x.subject=$1`, d.subject).Scan(&audits)
	_ = db.Pool.QueryRow(context.Background(), `SELECT issuer FROM public.accounts WHERE subject=$1`, d.subject).Scan(&issuer)
	if count != 1 || audits != 1 || issuer != "https://accounts.google.com" {
		t.Fatalf("accounts/audits/issuer %d/%d/%q", count, audits, issuer)
	}
}

func TestProviderCapacityRejectsWithoutQueue(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "provider-capacity"
	d.tokenGate = make(chan struct{})
	d.tokenStarted = make(chan struct{}, 8)
	gate := d.tokenGate
	defer func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	flows := make([]string, 9)
	bindings := make([]*http.Cookie, 9)
	for i := range flows {
		flows[i], bindings[i] = start(t, s, nil)
	}
	results := make(chan int, 8)
	for i := 0; i < 8; i++ {
		go func(i int) { results <- complete(s, flows[i], bindings[i]).Code }(i)
	}
	for i := 0; i < 8; i++ {
		select {
		case <-d.tokenStarted:
		case <-time.After(3 * time.Second):
			t.Fatal("provider requests did not fill admission")
		}
	}
	w := complete(s, flows[8], bindings[8])
	if w.Code != 503 {
		t.Fatalf("ninth callback was queued: %d", w.Code)
	}
	close(gate)
	for i := 0; i < 8; i++ {
		if status := <-results; status != 303 {
			t.Fatalf("admitted callback: %d", status)
		}
	}
}

func TestContinuationCapacityRefusesNewLogin(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	for i := 0; i < 1024; i++ {
		start(t, s, nil)
		time.Sleep(5 * time.Millisecond)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/auth/login", nil))
	if w.Code != 503 {
		t.Fatalf("full continuation store: %d", w.Code)
	}
}

func TestRestartCancelsUnfinishedLogin(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "restart-unfinished"
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	restarted := newTestServer(t, db.Pool, c, m, d.endpoints())
	w := complete(restarted, state, binding)
	if w.Code == 303 {
		t.Fatal("unfinished login survived restart")
	}
	var count int
	_ = db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.accounts WHERE subject=$1`, d.subject).Scan(&count)
	if count != 0 {
		t.Fatal("restart callback created account")
	}
}

func TestStockRemoteKeySetRetriesSequentialUnknownKeyMisses(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "unknown-key"
	d.tokenKeyID = "other-key"
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	for i := 0; i < 2; i++ {
		state, binding := start(t, s, nil)
		w := complete(s, state, binding)
		if w.Code == 303 {
			t.Fatal("unknown key accepted")
		}
	}
	d.mu.Lock()
	calls := d.calls
	d.mu.Unlock()
	if calls != 2 {
		t.Fatalf("sequential unknown-key misses fetched %d times, want two", calls)
	}
}

func TestGoogleTokenExchangeUsesOneCodeExchangeAttempt(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.tokenStatus = http.StatusUnauthorized
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	if w := complete(s, state, binding); w.Code != http.StatusUnauthorized {
		t.Fatalf("failed provider exchange status: %d", w.Code)
	}
	d.mu.Lock()
	calls := d.tokenCalls
	d.mu.Unlock()
	if calls != 1 {
		t.Fatalf("single-use Google code exchanged %d times", calls)
	}
}

func TestGoogleTokenAudienceMustBeOneExactScalar(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.audience = []string{"test-client"}
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	if w := complete(s, state, binding); w.Code != http.StatusUnauthorized {
		t.Fatalf("array audience was accepted: %d", w.Code)
	}
	var count int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.accounts WHERE subject=$1`, d.subject).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unverified account persisted: %d %v", count, err)
	}
}

func TestResolvedAccountRateIsBounded(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "rate-account"
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	state, binding := start(t, s, nil)
	w := complete(s, state, binding)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	session := sessionFrom(w)
	limited := false
	for i := 0; i < 30; i++ {
		r := httptest.NewRequest("GET", "/", nil)
		r.AddCookie(session)
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, r)
		if response.Code == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("resolved account rate did not refuse burst")
	}
}

func TestOversizeAndUnavailableProviderCannotRegister(t *testing.T) {
	for _, kind := range []string{"body", "header", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			c, m, db := fixture(t)
			d := newProvider(t)
			d.subject = "provider-" + kind
			d.oversizeBody = kind == "body"
			d.oversizeHeader = kind == "header"
			s := newTestServer(t, db.Pool, c, m, d.endpoints())
			state, binding := start(t, s, nil)
			if kind == "unavailable" {
				d.server.Close()
			}
			w := complete(s, state, binding)
			if w.Code == 303 {
				t.Fatal("provider failure accepted")
			}
			var count int
			_ = db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.accounts WHERE subject=$1`, d.subject).Scan(&count)
			if count != 0 {
				t.Fatal("provider failure persisted account")
			}
		})
	}
}

func TestSuccessfulCallbackPreservesSharedBrowserBindingForAnotherTab(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "two-tabs"
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	portal := httptest.NewTLSServer(s.Handler())
	defer portal.Close()
	jar, e := cookiejar.New(nil)
	if e != nil {
		t.Fatal(e)
	}
	client := portal.Client()
	client.Jar = jar
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	begin := func() string {
		t.Helper()
		response, e := client.Get(portal.URL + "/auth/login")
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		if response.StatusCode != 302 {
			t.Fatalf("login start: %d", response.StatusCode)
		}
		u, e := url.Parse(response.Header.Get("Location"))
		if e != nil {
			t.Fatal(e)
		}
		return u.Query().Get("state") + "|" + u.Query().Get("nonce")
	}
	finish := func(flow string) int {
		t.Helper()
		parts := strings.Split(flow, "|")
		response, e := client.Get(portal.URL + "/auth/callback?state=" + url.QueryEscape(parts[0]) + "&code=" + url.QueryEscape(parts[1]))
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		return response.StatusCode
	}
	first, second := begin(), begin()
	if status := finish(first); status != 303 {
		t.Fatalf("first tab: %d", status)
	}
	if status := finish(second); status != 303 {
		t.Fatalf("second tab after first callback: %d", status)
	}
}

func TestStockRemoteKeySetRefreshesForNewSigningKey(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "known-key-refresh"
	rotatedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	d.rotatedKey = rotatedKey
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	first, binding := start(t, s, nil)
	if w := complete(s, first, binding); w.Code != 303 {
		t.Fatalf("first login: %d", w.Code)
	}
	d.mu.Lock()
	d.keyID = "rotated-key"
	d.tokenKeyID = "rotated-key"
	d.mu.Unlock()
	second, binding := start(t, s, binding)
	if w := complete(s, second, binding); w.Code != 303 {
		t.Fatalf("new signing key was not loaded: %d", w.Code)
	}
	d.mu.Lock()
	calls := d.calls
	d.mu.Unlock()
	if calls != 2 {
		t.Fatalf("key fetches: got %d, want 2", calls)
	}
}

func TestRepeatedSuccessfulCallbacksRespectResolvedAccountRate(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "callback-rate"
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	first, binding := start(t, s, nil)
	if w := complete(s, first, binding); w.Code != 303 {
		t.Fatalf("initial callback: %d", w.Code)
	}
	refused := false
	for i := 0; i < 30; i++ {
		flow, nextBinding := start(t, s, binding)
		binding = nextBinding
		var before int
		if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.portal_sessions ps JOIN public.accounts a ON a.id=ps.account_id WHERE a.subject=$1`, d.subject).Scan(&before); e != nil {
			t.Fatal(e)
		}
		w := complete(s, flow, binding)
		if w.Code == 429 {
			var after int
			if e := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.portal_sessions ps JOIN public.accounts a ON a.id=ps.account_id WHERE a.subject=$1`, d.subject).Scan(&after); e != nil {
				t.Fatal(e)
			}
			if after != before {
				t.Fatalf("refused callback wrote session: %d to %d", before, after)
			}
			refused = true
			break
		}
		if w.Code != 303 {
			t.Fatalf("callback %d: %d", i, w.Code)
		}
	}
	if !refused {
		t.Fatal("resolved-account callback burst was not bounded")
	}
}

func TestExpiredAuthStateCleanupCatchesUpInBoundedBatches(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "cleanup-owner"
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	flow, binding := start(t, s, nil)
	if w := complete(s, flow, binding); w.Code != 303 {
		t.Fatalf("setup: %d", w.Code)
	}
	ctx := context.Background()
	_, e := db.Pool.Exec(ctx, `INSERT INTO public.authorization_transactions (id,state_digest,nonce_digest,binding_digest,pepper_version,created_at,expires_at)
 SELECT lpad(to_hex(i+100000),32,'0')::uuid,decode(md5('state'||i)||md5('state2'||i),'hex'),decode(repeat('aa',32),'hex'),decode(repeat('bb',32),'hex'),$1,clock_timestamp()-interval '3 hours',clock_timestamp()-interval '2 hours' FROM generate_series(1,1200) AS i`, m.CurrentPepper)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Pool.Exec(ctx, `INSERT INTO public.portal_sessions (credential_digest,pepper_version,account_id,credential_generation,csrf_digest,google_authenticated_at,created_at,last_used_at,idle_expires_at,absolute_expires_at)
 SELECT decode(md5('session'||i)||md5('session2'||i),'hex'),$1,a.id,0,decode(repeat('cc',32),'hex'),clock_timestamp()-interval '3 hours',clock_timestamp()-interval '3 hours',clock_timestamp()-interval '3 hours',clock_timestamp()-interval '2 hours',clock_timestamp()-interval '1 hour' FROM generate_series(1,1200) AS i CROSS JOIN public.accounts a WHERE a.subject=$2`, m.CurrentPepper, d.subject)
	if e != nil {
		t.Fatal(e)
	}
	maintainCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Maintain(maintainCtx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("cleanup did not stop after cancellation")
		}
	}()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		var flows, sessions int
		err1 := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.authorization_transactions WHERE expires_at<clock_timestamp()-interval '1 hour'`).Scan(&flows)
		err2 := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.portal_sessions WHERE idle_expires_at<clock_timestamp()-interval '1 hour'`).Scan(&sessions)
		if err1 != nil || err2 != nil {
			t.Fatalf("cleanup query: %v %v", err1, err2)
		}
		if flows == 0 && sessions == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	var flows, sessions int
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.authorization_transactions WHERE expires_at<clock_timestamp()-interval '1 hour'`).Scan(&flows)
	_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.portal_sessions WHERE idle_expires_at<clock_timestamp()-interval '1 hour'`).Scan(&sessions)
	t.Fatalf("cleanup backlog after catch-up window: flows=%d sessions=%d", flows, sessions)
}

func TestDatabaseOutageAfterVerifiedProviderResponseCreatesNoFallbackState(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "verified-before-db-outage"
	d.onKeys = func() { db.Pool.Close() }
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	flow, binding := start(t, s, nil)
	w := complete(s, flow, binding)
	if w.Code != 503 {
		t.Fatalf("callback after database loss: %d", w.Code)
	}
	if sessionFrom(w) != nil {
		t.Fatal("callback issued portal cookie without database commit")
	}
	d.mu.Lock()
	tokenCalls, keyCalls := d.tokenCalls, d.calls
	d.mu.Unlock()
	if tokenCalls != 1 || keyCalls != 1 {
		t.Fatalf("provider verification path not exercised: token=%d keys=%d", tokenCalls, keyCalls)
	}
	fresh, e := database.NewPool(context.Background(), c, m, false)
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.Close()
	var accounts, sessions int
	if e = fresh.QueryRow(context.Background(), `SELECT count(*) FROM public.accounts WHERE subject=$1`, d.subject).Scan(&accounts); e != nil {
		t.Fatal(e)
	}
	if e = fresh.QueryRow(context.Background(), `SELECT count(*) FROM public.portal_sessions ps JOIN public.accounts a ON a.id=ps.account_id WHERE a.subject=$1`, d.subject).Scan(&sessions); e != nil {
		t.Fatal(e)
	}
	if accounts != 0 || sessions != 0 {
		t.Fatalf("fallback state: accounts=%d sessions=%d", accounts, sessions)
	}
}
