// portal-browser-fixture is a local-only browser acceptance harness. It runs
// the production account handler and OAuth callback against a disposable DB,
// while substituting only the external identity provider endpoints.
package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/persistence/postgres"
)

func main() {
	configPath := flag.String("config", "", "serving config whose portal_origin is https://localhost:<port>")
	cert := flag.String("tls-cert", "", "certificate for localhost")
	key := flag.String("tls-key", "", "certificate private key")
	listen := flag.String("listen", "127.0.0.1:18443", "loopback TLS address")
	flag.Parse()
	if *configPath == "" || *cert == "" || *key == "" || !strings.HasPrefix(*listen, "127.0.0.1:") {
		fatal("config, TLS files, and a 127.0.0.1 listen address are required")
	}
	port := strings.TrimPrefix(*listen, "127.0.0.1:")
	c, err := config.Load(*configPath)
	if err != nil {
		fatal("load config: %v", err)
	}
	u, err := url.Parse(c.PortalOrigin)
	if err != nil || u.Scheme != "https" || u.Host != "localhost:"+port {
		fatal("portal_origin must match https://localhost:%s", port)
	}
	m, err := c.ReadMaterial()
	if err != nil {
		fatal("read config material: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := database.NewPool(ctx, c, m, false)
	if err != nil {
		fatal("connect disposable PostgreSQL: %v", err)
	}
	defer pool.Close()
	if err := database.VerifyServing(ctx, pool); err != nil {
		fatal("verify serving database: %v", err)
	}
	if err := auth.VerifyNativeOAuthKey(ctx, postgres.NewIssuerMaintenance(pool), m.NativeSigningKey); err != nil {
		fatal("verify native OAuth key: %v", err)
	}
	const accountID = "66a09395-51a0-451c-96b3-716e6038e870"
	_, err = pool.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id,recovery_review_hold) VALUES($1,'https://accounts.google.com','browser-acceptance','workspace-a@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp(),'Active','6dd09395-51a0-451c-96b3-716e6038e870',false) ON CONFLICT (issuer,subject) DO UPDATE SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870',recovery_review_hold=false`, accountID)
	if err != nil {
		fatal("seed disposable active test account: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO public.weekly_relay_usage(account_id,week_start,recorded_bytes,sequence,latest_operation_id,latest_delta_bytes)
SELECT $1::uuid,((date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-7*n)::date),
 CASE WHEN n IN (0,2) THEN 0 ELSE n*1048576+12345 END,
 CASE WHEN n IN (0,2) THEN 0 ELSE 1 END,
 CASE WHEN n IN (0,2) THEN NULL ELSE md5($1::text||n::text)::uuid END,
 CASE WHEN n IN (0,2) THEN 0 ELSE n*1048576+12345 END
FROM generate_series(0,12) AS weeks(n) WHERE n<>3
ON CONFLICT (account_id,week_start) DO UPDATE SET recorded_bytes=EXCLUDED.recorded_bytes,sequence=EXCLUDED.sequence,latest_operation_id=EXCLUDED.latest_operation_id,latest_delta_bytes=EXCLUDED.latest_delta_bytes`, accountID)
	if err != nil {
		fatal("seed disposable weekly usage history: %v", err)
	}
	for i := 1; i <= 55; i++ {
		planID := fmt.Sprintf("67000000-0000-4000-8000-%012x", i)
		if _, err = pool.Exec(ctx, `INSERT INTO public.quota_plans(id,name,weekly_bytes,sessions) VALUES($1,$2,1048576,3) ON CONFLICT(id) DO NOTHING`, planID, fmt.Sprintf("Browser plan %02d", i)); err != nil {
			fatal("seed disposable quota plan: %v", err)
		}
		accountID := fmt.Sprintf("66000000-0000-4000-8000-%012x", i)
		states := []string{"Pending", "Active", "Suspended", "Denied"}
		state := states[(i-1)%len(states)]
		recoveryHeld := i == 4
		if _, err = pool.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id,recovery_review_hold) VALUES($1,'https://accounts.google.com',$2,$3,true,clock_timestamp(),clock_timestamp(),clock_timestamp(),$4,'6dd09395-51a0-451c-96b3-716e6038e870',$5) ON CONFLICT(issuer,subject) DO UPDATE SET email=EXCLUDED.email,status=EXCLUDED.status,plan_id=EXCLUDED.plan_id,recovery_review_hold=EXCLUDED.recovery_review_hold`, accountID, fmt.Sprintf("browser-acceptance-%02d", i), fmt.Sprintf("acceptance-%02d@example.test", i), state, recoveryHeld); err != nil {
			fatal("seed disposable Relay Account: %v", err)
		}
	}
	provider, err := newIdentityProvider(m.GoogleClientID)
	if err != nil {
		fatal("prepare identity provider: %v", err)
	}
	identity := &http.Server{Addr: "127.0.0.1:18444", Handler: provider, ReadHeaderTimeout: 3 * time.Second}
	go func() {
		if err := identity.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fatal("identity provider: %v", err)
		}
	}()
	defer identity.Shutdown(context.Background())
	runtime := database.NewRuntime(pool)
	work := postgres.New(runtime)
	dependencies := auth.Dependencies{Work: work, AccountRegistration: work, PortalSessions: work, Authorization: work, Refresh: work, Deletions: postgres.NewDeletions(runtime), Recovery: postgres.NewRecovery(runtime), ReadViews: postgres.NewReadViews(runtime), Maintenance: postgres.NewMaintenance(runtime)}
	portal := auth.New(dependencies, c, m, auth.Provider{AuthorizationURL: "http://127.0.0.1:18444/authorize", TokenURL: "http://127.0.0.1:18444/token", KeysURL: "http://127.0.0.1:18444/keys"})
	portal.SetDiagnostics(diagnostic.New(os.Stdout))
	// The fixture has no live relay session manager; install the explicit
	// no-op adapter required to exercise guarded portal mutations through HTTP.
	portal.SetAccountChangedForTest(func(auth.AccountChange) string { return "" })
	portal.SetCapacitySampler(portal.SampleActiveGrants)
	zeroHash := strings.Repeat("0", 64)
	portal.SetDeletionJournal(&browserDeletionJournal{etag: "initial", head: auth.JournalHead{RepositoryID: "browser-acceptance", CoverageFloor: 0, CoverageHash: zeroHash, Hash: zeroHash, Format: 1}, events: make(map[string][]byte)}, "browser-acceptance", 0, zeroHash)
	// This browser harness has no data-plane or live Relay Sessions. Use the
	// fixture composition that supplies empty-session invalidation callbacks so
	// owner security mutations exercise their real HTTP and PostgreSQL paths.
	handler := portal.NativeHandlerForTest(m.NativeSigningKey)
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		fatal("listen: %v", err)
	}
	fmt.Printf("portal acceptance fixture ready at %s (identity provider 127.0.0.1:18444)\n", c.PortalOrigin)
	if err := (&http.Server{Handler: handler, ReadHeaderTimeout: 3 * time.Second}).ServeTLS(listener, *cert, *key); err != nil && err != http.ErrServerClosed {
		fatal("portal: %v", err)
	}
}

// browserDeletionJournal is a process-local journal double for exercising the
// production deletion admission and status handlers. It cannot complete or
// remove a deletion operation; lifecycle completion remains covered by the
// PostgreSQL deletion integration tests.
type browserDeletionJournal struct {
	mu     sync.Mutex
	head   auth.JournalHead
	etag   string
	events map[string][]byte
}

func (j *browserDeletionJournal) ReadHead(context.Context) (auth.JournalHead, string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.head, j.etag, nil
}

func (j *browserDeletionJournal) CreateEvent(_ context.Context, key string, data []byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, exists := j.events[key]; exists {
		return errors.New("event already exists")
	}
	j.events[key] = append([]byte(nil), data...)
	return nil
}

func (j *browserDeletionJournal) ReadEvent(_ context.Context, key string) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	data, exists := j.events[key]
	if !exists {
		return nil, errors.New("event not found")
	}
	return append([]byte(nil), data...), nil
}

func (j *browserDeletionJournal) ReplaceHead(_ context.Context, etag string, head auth.JournalHead) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if etag != j.etag {
		return errors.New("head condition failed")
	}
	j.head = head
	j.etag = fmt.Sprintf("%d", head.Sequence)
	return nil
}

func (j *browserDeletionJournal) ListEvents(context.Context, string) ([]string, string, error) {
	return nil, "", nil
}

type identityProvider struct {
	clientID string
	key      *rsa.PrivateKey
}

func newIdentityProvider(clientID string) (*identityProvider, error) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return &identityProvider{clientID, k}, nil
}
func (p *identityProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/authorize":
		callback, err := url.Parse(r.URL.Query().Get("redirect_uri"))
		if err != nil {
			http.Error(w, "bad callback", 400)
			return
		}
		states.Lock()
		states.nonce = r.URL.Query().Get("nonce")
		states.Unlock()
		q := callback.Query()
		q.Set("code", "local-browser-acceptance")
		q.Set("state", r.URL.Query().Get("state"))
		callback.RawQuery = q.Encode()
		http.Redirect(w, r, callback.String(), http.StatusFound)
	case "/token":
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", 400)
			return
		}
		states.Lock()
		nonce := states.nonce
		states.Unlock()
		claims := map[string]any{"iss": "https://accounts.google.com", "sub": "browser-acceptance", "aud": p.clientID, "exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": nonce, "email": "workspace-a@example.test", "email_verified": true, "hd": "example.test"}
		token, err := p.sign(claims)
		if err != nil {
			http.Error(w, "sign token", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture", "token_type": "Bearer", "expires_in": 300, "id_token": token})
	case "/keys":
		mod := base64.RawURLEncoding.EncodeToString(p.key.PublicKey.N.Bytes())
		exp := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(p.key.PublicKey.E)).Bytes())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "fixture", "n": mod, "e": exp}}})
	default:
		http.NotFound(w, r)
	}
}

var states = struct {
	sync.Mutex
	nonce string
}{}

func (p *identityProvider) sign(claims any) (string, error) {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "fixture"})
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func fatal(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...); os.Exit(1) }
