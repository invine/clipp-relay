package auth

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"clipp-relay/internal/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const sessionCookie = "__Host-clipp-portal"
const bindingCookie = "__Host-clipp-binding"
const googleIssuer = "https://accounts.google.com"

// Provider contains fixed, operator-owned endpoints. Only tests supply alternatives.
type Provider struct {
	AuthorizationURL, TokenURL, KeysURL string
	Client                              *http.Client
}

// SetAccountCommitHookForTest injects an account-mutation commit result so
// integration tests can exercise a lost acknowledgment after PostgreSQL commits.
// Serving code leaves this unset.
func (s *Server) SetAccountCommitHookForTest(commit func(context.Context, pgx.Tx) error) {
	s.commitAccount = commit
}

func Google() Provider {
	return Provider{AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", KeysURL: "https://www.googleapis.com/oauth2/v3/certs"}
}

type Server struct {
	Pool                               *pgxpool.Pool
	Origin, ClientID, ClientSecret     string
	AndroidRedirect, ExtensionRedirect string
	AdminAllowlistFile                 string
	Peppers                            map[uint64][]byte
	CurrentPepper                      uint64
	provider                           Provider
	client                             *http.Client
	providerSlots                      chan struct{}
	mu                                 sync.Mutex
	identityGuards                     [4096]sync.Mutex
	accountGuards                      sync.Map
	accountUnits                       chan struct{}
	uncertainAccounts                  sync.Map
	fenceKey                           [32]byte
	fences                             map[[32]byte]identityFence
	flowSeq                            uint64
	flowEpoch                          uint64
	keyMu                              sync.Mutex
	flows                              map[string]flow
	consents                           map[string]oauthConsent
	keys                               map[string]*rsa.PublicKey
	keysUntil                          time.Time
	lastKeyFetch                       time.Time
	globalRate                         rate
	accountRates                       map[string]rate
	beforeProfileRead                  func()
	commitPlan                         func(context.Context, pgx.Tx) error
	commitAccount                      func(context.Context, pgx.Tx) error
	commitDeletion                     func(context.Context, pgx.Tx) error
	capacitySampler                    func(context.Context, string) (CapacitySample, error)
	accountChanged                     func(AccountChange) func()
	deletionJournal                    DeletionJournal
	deletionRepository                 string
	deletionFloor                      int64
	deletionFloorHash                  string
	deletionWorker                     sync.Mutex
	deletionCompleted                  atomic.Uint64
	deletionRetried                    atomic.Uint64
	cleanupCompleted                   atomic.Uint64
	cleanupFailed                      atomic.Uint64
	cleanupWorker                      atomic.Bool
	cleanupCursor                      atomic.Uint64
	cleanupAccountCursors              []string
}

// WithAccountGuards orders all local account operations before their SQL row
// locks. Relay admission holds the same guard through registry installation.
func (s *Server) WithAccountGuards(ctx context.Context, ids []string, work func()) bool {
	select {
	case s.accountUnits <- struct{}{}:
		defer func() { <-s.accountUnits }()
	case <-ctx.Done():
		return false
	}
	ordered := append([]string(nil), ids...)
	sort.Strings(ordered)
	guards := make([]*sync.Mutex, 0, len(ordered))
	var previous string
	for _, id := range ordered {
		if len(guards) > 0 && id == previous {
			continue
		}
		previous = id
		value, _ := s.accountGuards.LoadOrStore(id, &sync.Mutex{})
		guard := value.(*sync.Mutex)
		guard.Lock()
		guards = append(guards, guard)
	}
	defer func() {
		for i := len(guards) - 1; i >= 0; i-- {
			guards[i].Unlock()
		}
	}()
	work()
	return true
}

// CapacitySample is a separate, aggregate live observation. Nil counts mean
// unavailable, not zero; it contains no Device Identity or session detail.
type CapacitySample struct{ LiveSessions, ActiveLoginGrants *int }

func (s *Server) SetCapacitySampler(sample func(context.Context, string) (CapacitySample, error)) {
	s.capacitySampler = sample
}

// SetAccountChanged installs the in-process invalidation path for committed
// account policy changes. The callback fences credit and detaches registry
// entries under the account guard, then returns connection-close I/O to run
// outside all logical guards. It must be set before serving HTTP requests.
type AccountChange struct {
	AccountID     string
	WeeklyBytes   int64
	SessionLimit  int
	CloseAll      bool
	DiscardCredit bool
}

func (s *Server) SetAccountChanged(changed func(AccountChange) func()) { s.accountChanged = changed }

// SampleActiveGrants reads only an aggregate live count, separately from the
// profile's policy and quota snapshot. The portal treats a failed sample as
// unavailable rather than as zero.
func (s *Server) SampleActiveGrants(parent context.Context, accountID string) (CapacitySample, error) {
	ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
	defer cancel()
	var count int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM public.login_grants g JOIN public.accounts a ON a.id=g.account_id WHERE g.account_id=$1 AND g.credential_generation=a.credential_generation AND g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp()`, accountID).Scan(&count)
	if err != nil {
		return CapacitySample{}, err
	}
	return CapacitySample{ActiveLoginGrants: &count}, nil
}

type rate struct {
	tokens float64
	at     time.Time
}

func (r *rate) allow(now time.Time, perSecond, burst float64) bool {
	if r.at.IsZero() {
		r.tokens = burst
		r.at = now
	}
	r.tokens += now.Sub(r.at).Seconds() * perSecond
	if r.tokens > burst {
		r.tokens = burst
	}
	r.at = now
	if r.tokens < 1 {
		return false
	}
	r.tokens--
	return true
}

var errRateLimit = errors.New("rate limited")
var errInvalidSession = errors.New("invalid session")

type flow struct {
	state, nonce, binding string
	expires               time.Time
	intent                *oauthIntent
	seq, epoch            uint64
}

type identityFence struct {
	seq   uint64
	until time.Time
}

// An ambiguous security commit may be released for a new Google login only
// when the expected committed revision and generation are seen in PostgreSQL.
type uncertainMutation struct {
	revision, generation int64
	status               string
}

func New(pool *pgxpool.Pool, c config.Config, m config.Material, provider Provider) *Server {
	client := provider.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{MaxResponseHeaderBytes: 16 << 10}}
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	s := &Server{Pool: pool, Origin: c.PortalOrigin, ClientID: m.GoogleClientID, ClientSecret: m.GoogleClientSecret, AndroidRedirect: c.PublicClients.AndroidRedirect, ExtensionRedirect: c.PublicClients.ExtensionRedirect, AdminAllowlistFile: c.Secrets.AdminAllowlistFile, Peppers: m.Peppers, CurrentPepper: m.CurrentPepper, provider: provider, client: client, providerSlots: make(chan struct{}, 8), accountUnits: make(chan struct{}, 64), flows: map[string]flow{}, consents: map[string]oauthConsent{}, fences: map[[32]byte]identityFence{}, keys: map[string]*rsa.PublicKey{}, accountRates: map[string]rate{}, cleanupAccountCursors: make([]string, len(cleanupQueries))}
	if _, err := rand.Read(s.fenceKey[:]); err != nil {
		panic("identity fence key unavailable")
	}
	return s
}
func (s *Server) identityKey(subject string) [32]byte {
	h := hmac.New(sha256.New, s.fenceKey[:])
	h.Write([]byte(googleIssuer + "\x00" + subject))
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}
func (s *Server) identityGuard(key [32]byte) *sync.Mutex {
	return &s.identityGuards[int(key[0])<<4|int(key[1]>>4)]
}

// WithIdentityFence serializes a security mutation against Google callback completion.
// It fences every older continuation even if work returns an uncertain error.
func (s *Server) WithIdentityFence(subject string, work func() error) error {
	return s.withIdentityGuard(subject, func() error {
		s.installIdentityFence(subject)
		return work()
	})
}

func (s *Server) withIdentityGuard(subject string, work func() error) error {
	key := s.identityKey(subject)
	guard := s.identityGuard(key)
	guard.Lock()
	defer guard.Unlock()
	return work()
}

// The caller holds the identity guard. Installing this after a security commit
// also fences flows started while the database transaction was in progress.
func (s *Server) installIdentityFence(subject string) {
	key := s.identityKey(subject)
	s.mu.Lock()
	now := time.Now()
	for key, fence := range s.fences {
		if !now.Before(fence.until) {
			delete(s.fences, key)
		}
	}
	if len(s.fences) >= 4096 && s.fences[key].until.IsZero() {
		s.flowEpoch++
		s.flows = map[string]flow{}
		s.consents = map[string]oauthConsent{}
		s.fences = map[[32]byte]identityFence{}
	}
	s.fences[key] = identityFence{seq: s.flowSeq, until: now.Add(10 * time.Minute)}
	s.mu.Unlock()
}
func opaque() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func uuid() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func (s *Server) digest(version uint64, purpose, value string) []byte {
	h := hmac.New(sha256.New, s.Peppers[version])
	h.Write([]byte("clipp-relay/v1/" + purpose + "\x00" + value))
	return h.Sum(nil)
}
func (s *Server) csrf(version uint64, credential string) string {
	return base64.RawURLEncoding.EncodeToString(s.digest(version, "csrf-token", credential))
}
func cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: age, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}
func fail(w http.ResponseWriter, status int) { http.Error(w, "Request could not be completed", status) }
func (s *Server) bounded(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request)) {
	if len(r.RequestURI) > 8192 || len(r.Header) > 128 || r.ContentLength > 16384 || (r.Method != http.MethodPost && r.ContentLength != 0) || r.Header.Get("Content-Encoding") != "" {
		fail(w, 413)
		return
	}
	s.mu.Lock()
	allowed := s.globalRate.allow(time.Now(), 200, 400)
	s.mu.Unlock()
	if !allowed {
		fail(w, 429)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	next(w, r.WithContext(ctx))
}
func (s *Server) allowAccount(subject string) bool {
	rateKey := base64.RawURLEncoding.EncodeToString(s.digest(s.CurrentPepper, "account-rate", googleIssuer+"\x00"+subject))
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if len(s.accountRates) >= 4096 {
		for k, v := range s.accountRates {
			if now.Sub(v.at) > 2*time.Minute {
				delete(s.accountRates, k)
			}
		}
		if len(s.accountRates) >= 4096 {
			return false
		}
	}
	v := s.accountRates[rateKey]
	ok := v.allow(now, 10, 20)
	s.accountRates[rateKey] = v
	return ok
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.home)
	mux.HandleFunc("GET /auth/login", s.login)
	mux.HandleFunc("GET /auth/callback", s.callback)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/authorize", s.confirmAuthorization)
	mux.HandleFunc("POST /oauth/token", s.token)
	mux.HandleFunc("POST /auth/logout", s.logout)
	mux.HandleFunc("POST /auth/revoke", s.revokeOwner)
	mux.HandleFunc("POST /auth/delete", s.deleteOwner)
	mux.HandleFunc("GET /auth/deletion/{id}", s.deletionStatus)
	mux.HandleFunc("GET /admin", s.adminHome)
	mux.HandleFunc("POST /admin/accounts/{id}", s.adminAccount)
	mux.HandleFunc("POST /admin/plans", s.adminPlan)
	mux.HandleFunc("POST /admin/plans/{id}/archive", s.adminArchivePlan)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.bounded(w, r, mux.ServeHTTP) })
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	s.loginFlow(w, r, nil)
}
func (s *Server) loginFlow(w http.ResponseWriter, r *http.Request, intent *oauthIntent) {
	select {
	case s.providerSlots <- struct{}{}:
		defer func() { <-s.providerSlots }()
	default:
		fail(w, 503)
		return
	}
	state, e := opaque()
	if e != nil {
		fail(w, 503)
		return
	}
	nonce, e := opaque()
	if e != nil {
		fail(w, 503)
		return
	}
	binding := ""
	if existing, e := r.Cookie(bindingCookie); e == nil && len(existing.Value) == 43 {
		binding = existing.Value
	}
	if binding == "" {
		binding, e = opaque()
		if e != nil {
			fail(w, 503)
			return
		}
	}
	id, e := uuid()
	if e != nil {
		fail(w, 503)
		return
	}
	s.mu.Lock()
	now := time.Now()
	for k, f := range s.flows {
		if now.After(f.expires) {
			delete(s.flows, k)
		}
	}
	if len(s.flows)+len(s.consents) >= 1024 {
		s.mu.Unlock()
		fail(w, 503)
		return
	}
	s.flowSeq++
	s.flows[state] = flow{state: state, nonce: nonce, binding: binding, expires: now.Add(10 * time.Minute), intent: intent, seq: s.flowSeq, epoch: s.flowEpoch}
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	_, e = s.Pool.Exec(ctx, `INSERT INTO public.authorization_transactions (id,state_digest,nonce_digest,binding_digest,pepper_version,created_at,expires_at) VALUES ($1,$2,$3,$4,$5,clock_timestamp(),clock_timestamp()+interval '10 minutes')`, id, s.digest(s.CurrentPepper, "google-state", state), s.digest(s.CurrentPepper, "google-nonce", nonce), s.digest(s.CurrentPepper, "browser-binding", binding), s.CurrentPepper)
	if e != nil {
		s.mu.Lock()
		delete(s.flows, state)
		s.mu.Unlock()
		fail(w, 503)
		return
	}
	cookie(w, bindingCookie, binding, 600)
	q := url.Values{"client_id": {s.ClientID}, "redirect_uri": {s.Origin + "/auth/callback"}, "response_type": {"code"}, "scope": {"openid email"}, "state": {state}, "nonce": {nonce}}
	http.Redirect(w, r, s.provider.AuthorizationURL+"?"+q.Encode(), http.StatusFound)
}
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	state, code := r.URL.Query().Get("state"), r.URL.Query().Get("code")
	if len(state) != 43 || code == "" || len(code) > 4096 || r.URL.Query().Has("error") {
		fail(w, 400)
		return
	}
	bind, e := r.Cookie(bindingCookie)
	if e != nil {
		fail(w, 400)
		return
	}
	select {
	case s.providerSlots <- struct{}{}:
		defer func() { <-s.providerSlots }()
	default:
		fail(w, 503)
		return
	}
	s.mu.Lock()
	f, ok := s.flows[state]
	if ok {
		delete(s.flows, state)
	}
	s.mu.Unlock()
	if !ok || time.Now().After(f.expires) || subtle.ConstantTimeCompare([]byte(bind.Value), []byte(f.binding)) != 1 {
		fail(w, 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	var claimed string
	e = s.Pool.QueryRow(ctx, `UPDATE public.authorization_transactions SET claimed_at=clock_timestamp() WHERE state_digest=$1 AND binding_digest=$2 AND nonce_digest=$3 AND pepper_version=$4 AND claimed_at IS NULL AND expires_at>clock_timestamp() RETURNING id`, s.digest(s.CurrentPepper, "google-state", state), s.digest(s.CurrentPepper, "browser-binding", bind.Value), s.digest(s.CurrentPepper, "google-nonce", f.nonce), s.CurrentPepper).Scan(&claimed)
	if e != nil {
		fail(w, 400)
		return
	}
	claims, e := s.exchange(ctx, code, f.nonce)
	if e != nil {
		fail(w, 401)
		return
	}
	if !time.Now().Before(f.expires) {
		fail(w, 401)
		return
	}
	key := s.identityKey(claims.Subject)
	guard := s.identityGuard(key)
	guard.Lock()
	defer guard.Unlock()
	s.mu.Lock()
	fence := s.fences[key]
	blocked := f.epoch != s.flowEpoch || (fence.seq >= f.seq && !fence.until.IsZero())
	s.mu.Unlock()
	if blocked {
		fail(w, 401)
		return
	}
	if !s.allowAccount(claims.Subject) {
		fail(w, 429)
		return
	}
	credential, e := opaque()
	if e != nil {
		fail(w, 503)
		return
	}
	account, e := uuid()
	if e != nil {
		fail(w, 503)
		return
	}
	auditID, e := uuid()
	if e != nil {
		fail(w, 503)
		return
	}
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		fail(w, 503)
		return
	}
	defer tx.Rollback(ctx)
	var accountID string
	var generation int64
	e = tx.QueryRow(ctx, `INSERT INTO public.accounts (id,issuer,subject,email,email_verified,hosted_domain,validated_at,created_at,last_portal_login_at) VALUES ($1,$2,$3,$4,$5,$6,clock_timestamp(),clock_timestamp(),clock_timestamp()) ON CONFLICT (issuer,subject) DO UPDATE SET email=EXCLUDED.email,email_verified=EXCLUDED.email_verified,hosted_domain=EXCLUDED.hosted_domain,validated_at=clock_timestamp(),last_portal_login_at=clock_timestamp() WHERE accounts.deletion_started_at IS NULL AND (accounts.status<>'Pending' OR accounts.last_portal_login_at>=clock_timestamp()-interval '90 days') RETURNING id,credential_generation`, account, googleIssuer, claims.Subject, claims.Email, claims.EmailVerified, claims.HostedDomain).Scan(&accountID, &generation)
	if e != nil {
		fail(w, 503)
		return
	}
	if pending, uncertain := s.uncertainAccounts.Load(accountID); uncertain {
		switch expected := pending.(type) {
		case uncertainMutation:
			var actualRevision, actualGeneration int64
			var actualStatus string
			if tx.QueryRow(ctx, `SELECT revision,credential_generation,status FROM public.accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&actualRevision, &actualGeneration, &actualStatus) != nil || actualRevision != expected.revision || actualGeneration != expected.generation || actualStatus != expected.status {
				fail(w, 503)
				return
			}
		case uncertainDeletion:
			var started *time.Time
			var operationCount int
			if tx.QueryRow(ctx, `SELECT deletion_started_at FROM public.accounts WHERE id=$1 FOR UPDATE`, accountID).Scan(&started) != nil || started != nil || tx.QueryRow(ctx, `SELECT count(*) FROM public.deletion_operations WHERE id=$1 AND account_id=$2`, expected.operationID, accountID).Scan(&operationCount) != nil || operationCount != 0 {
				fail(w, 503)
				return
			}
		default:
			fail(w, 503)
			return
		}
		// The account row lock resolves any in-flight admission or mutation.
		s.uncertainAccounts.Delete(accountID)
	}
	if accountID == account {
		if e = s.importRetainedUsage(ctx, tx, accountID, claims.Subject); e != nil {
			fail(w, 503)
			return
		}
		_, e = tx.Exec(ctx, `INSERT INTO public.audit_events (id,occurred_at,event,account_id) VALUES ($1,clock_timestamp(),'account_created',$2)`, auditID, accountID)
		if e != nil {
			fail(w, 503)
			return
		}
	}
	csrf := s.csrf(s.CurrentPepper, credential)
	_, e = tx.Exec(ctx, `INSERT INTO public.portal_sessions (credential_digest,pepper_version,account_id,credential_generation,csrf_digest,google_authenticated_at,created_at,last_used_at,idle_expires_at,absolute_expires_at) VALUES ($1,$2,$3,$4,$5,clock_timestamp(),clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '12 hours')`, s.digest(s.CurrentPepper, "portal-session", credential), s.CurrentPepper, accountID, generation, s.digest(s.CurrentPepper, "csrf-store", csrf))
	if e != nil {
		fail(w, 503)
		return
	}
	if e = tx.Commit(ctx); e != nil {
		fail(w, 503)
		return
	}
	cookie(w, sessionCookie, credential, 0)
	if f.intent != nil {
		s.presentConsent(w, r, accountID, credential, *f.intent)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type identity struct {
	Subject       string  `json:"sub"`
	Email         string  `json:"email"`
	EmailVerified bool    `json:"email_verified"`
	HostedDomain  *string `json:"hd"`
	Issuer        string  `json:"iss"`
	Audience      string  `json:"aud"`
	Nonce         string  `json:"nonce"`
	Expiry        int64   `json:"exp"`
	IssuedAt      int64   `json:"iat"`
}

func (s *Server) exchange(ctx context.Context, code, nonce string) (identity, error) {
	values := url.Values{"code": {code}, "client_id": {s.ClientID}, "client_secret": {s.ClientSecret}, "grant_type": {"authorization_code"}, "redirect_uri": {s.Origin + "/auth/callback"}}
	req, e := http.NewRequestWithContext(ctx, "POST", s.provider.TokenURL, strings.NewReader(values.Encode()))
	if e != nil {
		return identity{}, e
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, e := s.client.Do(req)
	if e != nil {
		return identity{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return identity{}, errors.New("provider unavailable")
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, 256<<10+1))
	if e != nil || len(body) > 256<<10 {
		return identity{}, errors.New("provider response oversized")
	}
	var token struct {
		IDToken string `json:"id_token"`
	}
	if json.Unmarshal(body, &token) != nil || len(token.IDToken) > 256<<10 {
		return identity{}, errors.New("provider response invalid")
	}
	return s.verify(ctx, token.IDToken, nonce)
}
func (s *Server) verify(ctx context.Context, raw, nonce string) (identity, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return identity{}, errors.New("token format")
	}
	headerBytes, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil || len(headerBytes) > 4096 {
		return identity{}, errors.New("token header")
	}
	var header struct {
		Algorithm string `json:"alg"`
		KeyID     string `json:"kid"`
		Type      string `json:"typ"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "RS256" || header.KeyID == "" || len(header.KeyID) > 256 {
		return identity{}, errors.New("token header")
	}
	key, e := s.key(ctx, header.KeyID)
	if e != nil {
		return identity{}, e
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		return identity{}, e
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, hash[:], signature) != nil {
		return identity{}, errors.New("token signature")
	}
	payload, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || len(payload) > 16384 {
		return identity{}, errors.New("token payload")
	}
	var c identity
	if json.Unmarshal(payload, &c) != nil {
		return identity{}, errors.New("token claims")
	}
	if c.Issuer != "accounts.google.com" && c.Issuer != googleIssuer {
		return identity{}, errors.New("issuer")
	}
	if c.Audience != s.ClientID || c.Nonce != nonce || c.Expiry <= time.Now().Unix() || c.IssuedAt > time.Now().Add(time.Minute).Unix() || c.Subject == "" || len(c.Subject) > 255 || c.Email == "" || len(c.Email) > 320 {
		return identity{}, errors.New("claims")
	}
	return c, nil
}
func (s *Server) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	now := time.Now()
	if now.Before(s.keysUntil) {
		if k := s.keys[kid]; k != nil {
			return k, nil
		}
	}
	if s.keys[kid] == nil && len(s.keys) > 0 && now.Sub(s.lastKeyFetch) < time.Minute {
		return nil, errors.New("key unavailable")
	}
	req, e := http.NewRequestWithContext(ctx, "GET", s.provider.KeysURL, nil)
	if e != nil {
		return nil, e
	}
	resp, e := s.client.Do(req)
	s.lastKeyFetch = now
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("keys unavailable")
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, 256<<10+1))
	if e != nil || len(body) > 256<<10 {
		return nil, errors.New("keys oversized")
	}
	var jwks struct {
		Keys []struct {
			Type      string `json:"kty"`
			Algorithm string `json:"alg"`
			Use       string `json:"use"`
			Kid       string `json:"kid"`
			N         string `json:"n"`
			E         string `json:"e"`
		} `json:"keys"`
	}
	if json.Unmarshal(body, &jwks) != nil || len(jwks.Keys) > 32 {
		return nil, errors.New("keys invalid")
	}
	keys := map[string]*rsa.PublicKey{}
	for _, j := range jwks.Keys {
		if j.Type != "RSA" || j.Algorithm != "RS256" || j.Use != "sig" || j.Kid == "" || len(j.Kid) > 256 {
			continue
		}
		nb, ne := base64.RawURLEncoding.DecodeString(j.N)
		eb, ee := base64.RawURLEncoding.DecodeString(j.E)
		if ne != nil || ee != nil || len(nb) > 512 || len(eb) > 4 {
			continue
		}
		ex := new(big.Int).SetBytes(eb).Int64()
		if ex < 3 || ex > 1<<31-1 {
			continue
		}
		keys[j.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: int(ex)}
	}
	if len(keys) == 0 {
		return nil, errors.New("keys empty")
	}
	age := time.Duration(0)
	noStore := false
	for _, directive := range strings.Split(resp.Header.Get("Cache-Control"), ",") {
		directive = strings.TrimSpace(directive)
		if directive == "no-store" || directive == "no-cache" {
			noStore = true
		}
		if strings.HasPrefix(directive, "max-age=") {
			seconds, e := strconv.ParseInt(strings.TrimPrefix(directive, "max-age="), 10, 64)
			if e == nil && seconds >= 0 && seconds <= 21600 {
				age = time.Duration(seconds) * time.Second
			} else if e == nil && seconds > 21600 {
				age = 6 * time.Hour
			}
		}
	}
	if ageSeconds, e := strconv.ParseInt(resp.Header.Get("Age"), 10, 64); e == nil && ageSeconds > 0 {
		if ageSeconds > 21600 {
			age = 0
		} else {
			age -= time.Duration(ageSeconds) * time.Second
		}
	}
	if noStore || age < 0 {
		age = 0
	}
	if age > 6*time.Hour {
		age = 6 * time.Hour
	}
	s.keys = keys
	s.keysUntil = now.Add(age)
	if k := keys[kid]; k != nil && age > 0 {
		return k, nil
	}
	return nil, errors.New("key unavailable")
}
func (s *Server) session(r *http.Request) (string, uint64, string, string, string, error) {
	c, e := r.Cookie(sessionCookie)
	if e != nil || len(c.Value) != 43 {
		return "", 0, "", "", "", errInvalidSession
	}
	for version := range s.Peppers {
		digest := s.digest(version, "portal-session", c.Value)
		var id, status, email, subject string
		var generation int64
		var storedVersion uint64
		var storedCSRF []byte
		e = s.Pool.QueryRow(r.Context(), `SELECT a.id,a.status,a.email,a.subject,a.credential_generation,ps.pepper_version,ps.csrf_digest FROM public.portal_sessions ps JOIN public.accounts a ON a.id=ps.account_id WHERE ps.credential_digest=$1 AND ps.pepper_version=$2 AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp() AND ps.credential_generation=a.credential_generation AND (a.status<>'Pending' OR a.last_portal_login_at>=clock_timestamp()-interval '90 days')`, digest, version).Scan(&id, &status, &email, &subject, &generation, &storedVersion, &storedCSRF)
		if e == nil {
			if _, uncertain := s.uncertainAccounts.Load(id); uncertain {
				return "", 0, "", "", "", errInvalidSession
			}
			if !s.allowAccount(subject) {
				return "", 0, "", "", "", errRateLimit
			}
			token := s.csrf(storedVersion, c.Value)
			if subtle.ConstantTimeCompare(storedCSRF, s.digest(storedVersion, "csrf-store", token)) != 1 {
				return "", 0, "", "", "", errors.New("csrf")
			}
			var extended bool
			e = s.Pool.QueryRow(r.Context(), `UPDATE public.portal_sessions ps SET last_used_at=clock_timestamp(),idle_expires_at=LEAST(absolute_expires_at,clock_timestamp()+interval '30 minutes') WHERE ps.credential_digest=$1 AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp() AND EXISTS (SELECT 1 FROM public.accounts a WHERE a.id=ps.account_id AND a.credential_generation=ps.credential_generation AND (a.status<>'Pending' OR a.last_portal_login_at>=clock_timestamp()-interval '90 days')) RETURNING true`, digest).Scan(&extended)
			if e == pgx.ErrNoRows {
				return "", 0, "", "", "", errInvalidSession
			}
			if e != nil {
				return "", 0, "", "", "", e
			}
			return id, version, status, email, token, nil
		}
		if e != pgx.ErrNoRows {
			return "", 0, "", "", "", e
		}
	}
	return "", 0, "", "", "", errInvalidSession
}

type quotaHistory struct {
	Week      string `json:"week"`
	Committed int64  `json:"committed"`
}

var page = template.Must(template.New("portal").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Clipp Relay</title><style>body{font:16px system-ui;background:#f8fafc;color:#172033;margin:0}.shell{max-width:720px;margin:8vh auto;padding:32px;background:white;border:1px solid #e2e8f0;border-radius:18px;box-shadow:0 12px 36px #1720330d}.badge{display:inline-block;border-radius:999px;background:#fff4d6;color:#785500;padding:6px 12px;font-weight:650}button,.button{background:#183f75;color:white;border:0;border-radius:9px;padding:11px 17px;font:inherit;text-decoration:none;cursor:pointer}small{color:#64748b}.doughnut{width:130px;height:130px;border-radius:50%;background:conic-gradient(#183f75 var(--used),#dbeafe 0);display:grid;place-items:center}.doughnut::before{content:'';width:82px;height:82px;background:white;grid-area:1/1}.doughnut span{z-index:1;grid-area:1/1;font-weight:700}table{border-collapse:collapse;width:100%}td,th{text-align:left;padding:5px;border-bottom:1px solid #e2e8f0}</style><main class="shell"><small>Clipp Relay</small>{{if .SignedIn}}<h1>Your relay account</h1><p class="badge">{{.Status}}</p><p>{{.Description}}</p><p><small>{{.Email}}</small></p>{{if .Active}}<section><h2>Your allowance</h2><p>Weekly Quota Committed allowance: {{.WeeklyBytes}} bytes</p><p>Concurrent Relay Sessions: {{.Sessions}}</p><h3>Account capacity</h3><table><tr><th>Resource</th><th>Live count and limit</th></tr><tr><td>Relay Sessions</td><td>{{if .LiveSessionsKnown}}{{.LiveSessions}} of {{.Sessions}}{{else}}Unavailable · limit {{.Sessions}}{{end}}</td></tr><tr><td>Login Grants</td><td>{{if .ActiveGrantsKnown}}{{.ActiveGrants}} of {{.GrantLimit}}{{else}}Unavailable · limit {{.GrantLimit}}{{end}}</td></tr></table><small>Live counts are sampled separately from quota history.</small><div class="doughnut" style="--used:{{.Percent}}%" role="img" aria-label="Quota committed {{.Committed}} of {{.WeeklyBytes}} bytes"><span>{{.Percent}}%</span></div><p>Quota committed: {{.Committed}} bytes</p><p>{{.UsageState}}</p><small>Quota committed includes unused funded credit and is not measured traffic. Unused funded credit is lost on restart and never refunded.</small><h3>Weekly history</h3><table><tr><th>Week starting UTC</th><th>Quota committed</th></tr>{{range .History}}<tr><td>{{.Week}}</td><td>{{.Committed}} bytes</td></tr>{{end}}</table></section>{{end}}{{if .Admin}}<p><a href="/admin">Administration</a></p>{{end}}<form method="post" action="/auth/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Log out</button></form><form method="post" action="/auth/revoke"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Sign out everywhere</button></form><form method="post" action="/auth/delete"><input type="hidden" name="csrf" value="{{.CSRF}}"><button>Delete relay account</button><small>Deletion is permanent. Sign in with Google again if your last authentication was more than ten minutes ago.</small></form>{{else}}<h1>Relay account</h1><p>Sign in with Google to view your account status.</p><a class="button" href="/auth/login">Sign in with Google</a>{{end}}</main></html>`))

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	id, version, _, _, csrf, e := s.session(r)
	if errors.Is(e, errRateLimit) {
		fail(w, 429)
		return
	}
	if e != nil && !errors.Is(e, errInvalidSession) {
		fail(w, 503)
		return
	}
	var status, email string
	var weeklyBytes *int64
	var sessions *int
	var verified bool
	var hd *string
	var committed int64
	var historyJSON []byte
	if e == nil {
		if s.beforeProfileRead != nil {
			s.beforeProfileRead()
		}
		err := s.Pool.QueryRow(r.Context(), `WITH current_week AS MATERIALIZED (SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date AS monday)
SELECT a.status,a.email,a.email_verified,a.hosted_domain,COALESCE(a.weekly_bytes_override,p.weekly_bytes),COALESCE(a.sessions_override,p.sessions),
COALESCE((SELECT u.committed_bytes FROM public.weekly_quota_usage u WHERE u.account_id=a.id AND u.week_start=(SELECT monday FROM current_week)),0),
(SELECT jsonb_agg(jsonb_build_object('week',w.week_start,'committed',COALESCE(u.committed_bytes,0)) ORDER BY w.week_start DESC)
 FROM (SELECT ((SELECT monday FROM current_week)-7*n)::date AS week_start FROM generate_series(0,12) n) w
 LEFT JOIN public.weekly_quota_usage u ON u.account_id=a.id AND u.week_start=w.week_start)
FROM public.portal_sessions ps JOIN public.accounts a ON a.id=ps.account_id
LEFT JOIN public.quota_plans p ON p.id=a.plan_id
WHERE a.id=$1 AND ps.credential_digest=$2 AND ps.pepper_version=$3
AND ps.credential_generation=a.credential_generation
AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp()
AND (a.status<>'Pending' OR a.last_portal_login_at>=clock_timestamp()-interval '90 days')`, id, s.sessionDigest(r, version), version).Scan(&status, &email, &verified, &hd, &weeklyBytes, &sessions, &committed, &historyJSON)
		if err == pgx.ErrNoRows {
			e = errInvalidSession
		} else if err != nil {
			fail(w, 503)
			return
		}
		if e == nil && status == "Active" && (weeklyBytes == nil || sessions == nil) {
			fail(w, 503)
			return
		}
	}
	admin := false
	if e == nil {
		if policy, err := config.LoadAdminAllowlist(s.AdminAllowlistFile); err == nil {
			admin = policy.Allows(email, verified, deref(hd))
		}
	}
	description := map[string]string{"Pending": "Your account is waiting for approval.", "Active": "Your account is active.", "Suspended": "Your account is temporarily unavailable.", "Denied": "Your account is not approved."}[status]
	var bytesValue int64
	var sessionsValue int
	if weeklyBytes != nil {
		bytesValue = *weeklyBytes
	}
	if sessions != nil {
		sessionsValue = *sessions
	}
	var history []quotaHistory
	if e == nil && status == "Active" && json.Unmarshal(historyJSON, &history) != nil {
		fail(w, 503)
		return
	}
	var liveSessions, activeGrants int
	var liveKnown, grantsKnown bool
	if e == nil && status == "Active" && s.capacitySampler != nil {
		if sample, err := s.capacitySampler(r.Context(), id); err == nil {
			if sample.LiveSessions != nil && *sample.LiveSessions >= 0 {
				liveSessions = *sample.LiveSessions
				liveKnown = true
			}
			if sample.ActiveLoginGrants != nil && *sample.ActiveLoginGrants >= 0 {
				activeGrants = *sample.ActiveLoginGrants
				grantsKnown = true
			}
		}
	}
	percent := int64(0)
	usageState := "No Quota committed this week."
	if bytesValue == 0 {
		usageState = "Zero allowance: relay use is unavailable."
		if committed > 0 {
			usageState += " Quota committed exceeds the current zero allowance."
		}
	} else if committed >= bytesValue {
		percent = 100
		if committed > bytesValue {
			usageState = "Quota committed exceeds the current allowance after a plan reduction."
		} else {
			usageState = "Weekly allowance fully committed."
		}
	} else {
		percent = int64(float64(committed) * 100 / float64(bytesValue))
		usageState = "Remaining allowance: " + strconv.FormatInt(bytesValue-committed, 10) + " bytes."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, struct {
		SignedIn                               bool
		Status, Description, Email, CSRF       string
		Active, Admin                          bool
		WeeklyBytes                            int64
		Sessions                               int
		Committed, Percent                     int64
		UsageState                             string
		History                                []quotaHistory
		LiveSessions, ActiveGrants, GrantLimit int
		LiveSessionsKnown, ActiveGrantsKnown   bool
	}{e == nil, status, description, email, csrf, status == "Active", admin, bytesValue, sessionsValue, committed, percent, usageState, history, liveSessions, activeGrants, 20, liveKnown, grantsKnown})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.Origin {
		fail(w, 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		fail(w, 400)
		return
	}
	_, version, _, _, csrf, e := s.session(r)
	if errors.Is(e, errRateLimit) {
		fail(w, 429)
		return
	}
	if e != nil && !errors.Is(e, errInvalidSession) {
		fail(w, 503)
		return
	}
	if e != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(csrf)) != 1 {
		fail(w, 403)
		return
	}
	c, _ := r.Cookie(sessionCookie)
	digest := s.digest(version, "portal-session", c.Value)
	_, e = s.Pool.Exec(r.Context(), `DELETE FROM public.portal_sessions WHERE credential_digest=$1`, digest)
	if e != nil {
		fail(w, 503)
		return
	}
	cookie(w, sessionCookie, "", -1)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) revokeOwner(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.Origin {
		fail(w, 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		fail(w, 400)
		return
	}
	id, version, _, _, csrf, err := s.session(r)
	if err != nil {
		fail(w, 401)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(csrf)) != 1 {
		fail(w, 403)
		return
	}
	var subject string
	if err := s.Pool.QueryRow(r.Context(), `SELECT subject FROM public.accounts WHERE id=$1`, id).Scan(&subject); err != nil {
		fail(w, 503)
		return
	}
	var applied, uncertain bool
	var closeConnections func()
	_ = s.withIdentityGuard(subject, func() error {
		if !s.WithAccountGuards(r.Context(), []string{id}, func() {
			ctx := r.Context()
			tx, e := s.Pool.Begin(ctx)
			if e != nil {
				fail(w, 503)
				return
			}
			defer tx.Rollback(ctx)
			var locked, status string
			var accountRevision, oldGeneration int64
			e = tx.QueryRow(ctx, `SELECT id,status,revision,credential_generation FROM public.accounts WHERE id=$1 FOR UPDATE`, id).Scan(&locked, &status, &accountRevision, &oldGeneration)
			if e != nil {
				fail(w, 503)
				return
			}
			var live bool
			e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.portal_sessions ps JOIN public.accounts a ON a.id=ps.account_id WHERE ps.account_id=$1 AND ps.credential_digest=$2 AND ps.pepper_version=$3 AND ps.credential_generation=a.credential_generation AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp())`, id, s.sessionDigest(r, version), version).Scan(&live)
			if e != nil || !live {
				fail(w, 401)
				return
			}
			e = revokeAccountCredentials(ctx, tx, id)
			if e == nil {
				var auditID string
				auditID, e = uuid()
				if e == nil {
					_, e = tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id) VALUES($1,clock_timestamp(),'owner_credentials_revoked',$2)`, auditID, id)
				}
			}
			if e != nil {
				fail(w, 503)
				return
			}
			if s.commitAccount != nil {
				e = s.commitAccount(ctx, tx)
			} else {
				e = tx.Commit(ctx)
			}
			s.installIdentityFence(subject)
			if e != nil {
				s.uncertainAccounts.Store(id, uncertainMutation{revision: accountRevision, generation: oldGeneration + 1, status: status})
			}
			if s.accountChanged != nil {
				closeConnections = s.accountChanged(AccountChange{AccountID: id, CloseAll: true, DiscardCredit: true})
			}
			applied, uncertain = true, e != nil
		}) {
			fail(w, 503)
		}
		return nil
	})
	if applied {
		if closeConnections != nil {
			closeConnections()
		}
		cookie(w, sessionCookie, "", -1)
		if uncertain {
			uncertainResponse(w)
		} else {
			http.Redirect(w, r, "/", http.StatusSeeOther)
		}
	}
}
