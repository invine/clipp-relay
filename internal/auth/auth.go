package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"clipp-relay/internal/auth/issuer"
	"clipp-relay/internal/config"
	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/portal"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
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
func (s *Server) SetAccountCommitHookForTest(commit func(context.Context, persistence.AccountAccessTransaction) error) {
	s.commitAccountAccess = commit
}

// SetRelayCredentialHookForTest controls the gap after a credential observation
// and before protected admission. Production leaves this unset.
func (s *Server) SetRelayCredentialHookForTest(hook func()) { s.relayCredentialHook = hook }

// SetCredentialCommitHookForTest injects a lost credential commit acknowledgment.
// Production leaves this unset.
func (s *Server) SetCredentialCommitHookForTest(commit func(context.Context, persistence.Transaction) error) {
	s.commitRefresh = commit
	s.commitAuthorization = commit
}

// SetAuthorizationCommitHookForTest injects storage-neutral commit outcomes for
// native authorization tests. Production leaves this unset.
func (s *Server) SetAuthorizationCommitHookForTest(commit func(context.Context, persistence.Transaction) error) {
	s.commitAuthorization = commit
}

// SetPortalCommitHookForTest injects a lost portal-session commit acknowledgment.
// Production leaves this unset.
func (s *Server) SetPortalCommitHookForTest(commit func(context.Context, persistence.Transaction) error) {
	s.commitPortal = commit
}

func Google() Provider {
	return Provider{AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", KeysURL: "https://www.googleapis.com/oauth2/v3/certs"}
}

type Server struct {
	diagnostics                        *diagnostic.Logger
	transitions                        *diagnostic.Transitions
	persistence                        persistence.Work
	accountRegistration                persistence.AccountRegistrationWork
	portalSessions                     persistence.PortalSessionWork
	deletions                          persistence.DeletionRepository
	recovery                           persistence.RecoveryRepository
	readViews                          persistence.ReadViews
	maintenance                        persistence.Maintenance
	Origin, ClientID, ClientSecret     string
	AndroidRedirect, ExtensionRedirect string
	AdminAllowlistFile                 string
	Peppers                            map[uint64][]byte
	CurrentPepper                      uint64
	provider                           Provider
	client                             *http.Client
	providerSlots                      chan struct{}
	googleOAuth                        *oauth2.Config
	googleVerifier                     *oidc.IDTokenVerifier
	mu                                 sync.Mutex
	identityGuards                     [4096]sync.Mutex
	accountGuards                      sync.Map
	uncertainAccounts                  sync.Map
	fenceKey                           [32]byte
	fences                             map[[32]byte]identityFence
	flowSeq                            uint64
	flowEpoch                          uint64
	flows                              map[string]flow
	consents                           map[string]oauthConsent
	globalRate                         rate
	accountRates                       map[string]rate
	beforeProfileRead                  func()
	beforeAdminDetailsRead             func()
	beforeAdminRecheck                 func()
	beforeOwnerSubjectRead             func()
	beforeAccountActionGuard           func()
	beforeOwnerDeletionRecheck         func(context.Context) error
	beforeOwnerAccessRecheck           func(context.Context) error
	commitPlanAccess                   func(context.Context, persistence.AccountAccessTransaction) error
	commitAccountAccess                func(context.Context, persistence.AccountAccessTransaction) error
	relayCredentialHook                func()
	commitAuthorization                func(context.Context, persistence.Transaction) error
	commitRefresh                      func(context.Context, persistence.Transaction) error
	nativeIssuer                       *issuer.Issuer
	commitPortal                       func(context.Context, persistence.Transaction) error
	commitDeletionAdmission            func(context.Context, persistence.DeletionTransaction) error
	commitDeletionRepository           func(context.Context, persistence.DeletionTransaction) error
	commitRecovery                     func(context.Context, persistence.RecoveryTransaction) error
	capacitySampler                    func(context.Context, string) (CapacitySample, error)
	accountChanged                     func(AccountChange) string
	accountInvalidated                 func(string, string) error
	accountInvalidationFallback        func(string, string) error
	accountInvalidationComplete        func(string) error
	grantRevoked                       func(string) error
	grantRevocationFallback            func(string) error
	grantRevocationComplete            func(string) error
	deletionAccounting                 QuotaProtection
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

func (s *Server) withAccountGuards(ctx context.Context, ids []string, work func(context.Context)) error {
	if s.accountChanged == nil {
		return errors.New("relay account protection not configured")
	}
	executed := false
	var workErr error
	op, err := s.persistence.Start(ctx)
	if err != nil {
		return err
	}
	defer op.Close()
	unitCtx := op.Context()
	{
		ordered := append([]string(nil), ids...)
		sort.Strings(ordered)
		guards := make([]chan struct{}, 0, len(ordered))
		defer func() {
			for i := len(guards) - 1; i >= 0; i-- {
				<-guards[i]
			}
		}()
		var previous string
		for _, id := range ordered {
			if len(guards) > 0 && id == previous {
				continue
			}
			previous = id
			value, _ := s.accountGuards.LoadOrStore(id, make(chan struct{}, 1))
			guard := value.(chan struct{})
			select {
			case guard <- struct{}{}:
				guards = append(guards, guard)
			case <-unitCtx.Done():
				return unitCtx.Err()
			}
		}
		if err := unitCtx.Err(); err != nil {
			return err
		}
		executed = true
		work(unitCtx)
		workErr = unitCtx.Err()
	}
	if workErr != nil {
		return workErr
	}
	if !executed {
		return errors.New("account guard work did not execute")
	}
	return nil
}

// CapacitySample is a separate, aggregate live observation. Nil counts mean
// unavailable, not zero; it contains no Device Identity or session detail.
type CapacitySample struct{ LiveSessions, ActiveLoginGrants *int }

func (s *Server) SetCapacitySampler(sample func(context.Context, string) (CapacitySample, error)) {
	s.capacitySampler = sample
}

// AccountChange describes the authoritative policy outcome of a protected
// Auth command. Local invalidation runs under account protection; connection
// close I/O is deferred until all logical guards have been released.
type AccountChange struct {
	AccountID    string
	WeeklyBytes  int64
	SessionLimit int
	CloseAll     bool
}

// SetAccountChangedForTest installs a controlled invalidation adapter. Production
// must use ConfigureRelayProtection, which owns the complete policy operation.
func (s *Server) SetAccountChangedForTest(changed func(AccountChange) string) {
	s.accountChanged = changed
	if s.accountInvalidated == nil {
		s.accountInvalidated = func(string, string) error { return nil }
	}
	if s.accountInvalidationFallback == nil {
		s.accountInvalidationFallback = func(string, string) error { return nil }
	}
	if s.accountInvalidationComplete == nil {
		s.accountInvalidationComplete = func(string) error { return nil }
	}
}

// SetAccountInvalidatedForTest wraps the primary account notification while
// retaining the configured manager as its fail-closed fallback.
func (s *Server) SetAccountInvalidatedForTest(invalidated func(string) error) {
	s.accountInvalidated = func(id, _ string) error { return invalidated(id) }
	if s.accountInvalidationFallback == nil {
		s.accountInvalidationFallback = func(id, _ string) error { return invalidated(id) }
	}
}

func (s *Server) SetAccountInvalidationFallbackForTest(invalidated func(string) error) {
	s.accountInvalidationFallback = func(id, _ string) error { return invalidated(id) }
}

func (s *Server) SetAccountInvalidationCompleteForTest(complete func(string) error) {
	s.accountInvalidationComplete = complete
}

// RelaySessions exposes physical operations, not account policy decisions.
type RelaySessions interface {
	AccountSessions(string) int
	RetireAccount(string, string) error
	CompleteAccountInvalidation(string) error
	RetireGrant(string) error
	CompleteGrantInvalidation(string) error
}

// SetGrantRevokedForTest installs the grant invalidation recipient for isolated
// Auth tests. Production configures it through ConfigureRelayProtection.
func (s *Server) SetGrantRevokedForTest(revoked func(string) error) {
	s.grantRevoked = revoked
	if s.grantRevocationFallback == nil {
		s.grantRevocationFallback = revoked
	}
	if s.grantRevocationComplete == nil {
		s.grantRevocationComplete = func(string) error { return nil }
	}
}

// QuotaProtection applies current policy without discarding observations.
type QuotaProtection interface {
	ApplyAccountingPolicy(string, int64) bool
	BeginAccountingDeletion(string)
	FlushDeletionAccounting(context.Context, string) error
	ForgetAccounting(string)
}

// ConfigureRelayProtection installs the complete observed policy
// operation. Policy collaboration is independent of presentation queries.
func (s *Server) ConfigureRelayProtection(accounting QuotaProtection, sessions RelaySessions) error {
	if accounting == nil || sessions == nil {
		return errors.New("relay account protection requires quota and sessions")
	}
	s.deletionAccounting = accounting
	s.grantRevoked = func(grantID string) error {
		return sessions.RetireGrant(grantID)
	}
	// RetireGrant is synchronous logical invalidation. Physical closure is
	// separately released by Auth after its identity protection is unlocked.
	s.grantRevocationFallback = func(grantID string) error { return sessions.RetireGrant(grantID) }
	s.grantRevocationComplete = sessions.CompleteGrantInvalidation
	s.accountInvalidated = sessions.RetireAccount
	s.accountInvalidationFallback = sessions.RetireAccount
	s.accountInvalidationComplete = sessions.CompleteAccountInvalidation
	s.accountChanged = func(change AccountChange) string {
		exhausted := accounting.ApplyAccountingPolicy(change.AccountID, change.WeeklyBytes)
		if change.CloseAll {
			return "account_access_changed"
		}
		if exhausted {
			return "quota_exhausted"
		}
		if sessions.AccountSessions(change.AccountID) > change.SessionLimit {
			return "policy_changed"
		}
		return ""
	}
	s.SetCapacitySampler(func(ctx context.Context, account string) (CapacitySample, error) {
		sample, err := s.SampleActiveGrants(ctx, account)
		if err != nil {
			return sample, err
		}
		count := sessions.AccountSessions(account)
		sample.LiveSessions = &count
		return sample, nil
	})
	return nil
}

// deliverAccountInvalidation synchronously applies a confirmed account access
// change to Session Manager. Cancellation of pending validation is independent
// of the initiating HTTP request, and duplicate delivery is safe.
func (s *Server) deliverAccountInvalidation(accountID, reason string) error {
	if accountID == "" || s.accountInvalidated == nil || s.accountInvalidationFallback == nil {
		return errors.New("account invalidation is not configured")
	}
	if reason == "account_access_changed" && s.nativeIssuer != nil {
		s.nativeIssuer.CancelPendingValidations()
	}
	if err := s.accountInvalidated(accountID, reason); err == nil {
		return nil
	}
	return s.accountInvalidationFallback(accountID, reason)
}

// SampleActiveGrants reads only an aggregate live count, separately from the
// profile's policy and quota snapshot. The portal treats a failed sample as
// unavailable rather than as zero.
func (s *Server) SampleActiveGrants(parent context.Context, accountID string) (CapacitySample, error) {
	ctx, cancel := context.WithTimeout(parent, 500*time.Millisecond)
	defer cancel()
	count, err := s.readViews.ActiveLoginGrantCount(ctx, accountID)
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

// Dependencies are selected by startup composition. Functional Auth has no
// PostgreSQL constructor or driver handle.
type Dependencies struct {
	Work                persistence.Work
	AccountRegistration persistence.AccountRegistrationWork
	PortalSessions      persistence.PortalSessionWork
	Deletions           persistence.DeletionRepository
	Recovery            persistence.RecoveryRepository
	ReadViews           persistence.ReadViews
	Maintenance         persistence.Maintenance
}

func New(deps Dependencies, c config.Config, m config.Material, provider Provider) *Server {
	httpSlots := make(chan struct{}, 8)
	client := newProviderHTTPClient(provider.Client, httpSlots)
	googleOAuth := &oauth2.Config{
		ClientID:     m.GoogleClientID,
		ClientSecret: m.GoogleClientSecret,
		RedirectURL:  c.PortalOrigin + "/auth/callback",
		Scopes:       []string{oidc.ScopeOpenID, oidc.ScopeEmail},
		Endpoint: oauth2.Endpoint{
			AuthURL:   provider.AuthorizationURL,
			TokenURL:  provider.TokenURL,
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
	keySet := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), provider.KeysURL)
	googleVerifier := oidc.NewVerifier(googleIssuer, keySet, &oidc.Config{ClientID: m.GoogleClientID, SupportedSigningAlgs: []string{"RS256"}})
	s := &Server{persistence: deps.Work, accountRegistration: deps.AccountRegistration, portalSessions: deps.PortalSessions, deletions: deps.Deletions, recovery: deps.Recovery, readViews: deps.ReadViews, maintenance: deps.Maintenance, Origin: c.PortalOrigin, ClientID: m.GoogleClientID, ClientSecret: m.GoogleClientSecret, AndroidRedirect: c.PublicClients.AndroidRedirect, ExtensionRedirect: c.PublicClients.ExtensionRedirect, AdminAllowlistFile: c.Secrets.AdminAllowlistFile, Peppers: m.Peppers, CurrentPepper: m.CurrentPepper, provider: provider, client: client, providerSlots: make(chan struct{}, 8), googleOAuth: googleOAuth, googleVerifier: googleVerifier, flows: map[string]flow{}, consents: map[string]oauthConsent{}, fences: make(map[[32]byte]identityFence), accountRates: make(map[string]rate), cleanupAccountCursors: make([]string, cleanupOperationCount)}
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

func lockIdentity(ctx context.Context, guard *sync.Mutex) bool {
	for !guard.TryLock() {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(5 * time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		guard.Unlock()
		return false
	}
	return true
}

// WithIdentityFence serializes a security mutation against Google callback completion.
// It fences every older continuation even if work returns an uncertain error.
func (s *Server) WithIdentityFence(subject string, work func() error) error {
	guard := s.identityGuard(s.identityKey(subject))
	guard.Lock()
	defer guard.Unlock()
	s.installIdentityFence(subject)
	return work()
}

func (s *Server) withIdentityGuard(ctx context.Context, subject string, work func(context.Context) error) error {
	op, err := s.persistence.Start(ctx)
	if err != nil {
		return err
	}
	defer op.Close()
	unitCtx := op.Context()
	key := s.identityKey(subject)
	guard := s.identityGuard(key)
	if !lockIdentity(unitCtx, guard) {
		return unitCtx.Err()
	}
	defer guard.Unlock()
	if err := work(unitCtx); err != nil {
		return err
	}
	return unitCtx.Err()
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
func fail(w http.ResponseWriter, status int) { portal.Error(w, status) }
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
	portal.RegisterAssets(mux)
	mux.HandleFunc("GET /", s.home)
	mux.HandleFunc("GET /account/security", s.home)
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
	mux.HandleFunc("GET /admin/plans", s.adminPlanLibrary)
	mux.HandleFunc("POST /admin/accounts/{id}", s.adminAccount)
	mux.HandleFunc("POST /admin/plans", s.adminPlan)
	mux.HandleFunc("POST /admin/plans/{id}/archive", s.adminArchivePlan)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/oauth/token" && (s.grantRevoked == nil || s.grantRevocationFallback == nil || s.grantRevocationComplete == nil) {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		if r.Method == http.MethodPost && (r.URL.Path == "/auth/revoke" || r.URL.Path == "/auth/delete" || strings.HasPrefix(r.URL.Path, "/admin/accounts/")) && (s.accountChanged == nil || s.accountInvalidated == nil || s.accountInvalidationFallback == nil || s.accountInvalidationComplete == nil) {
			fail(w, http.StatusServiceUnavailable)
			return
		}
		s.observedRequest(w, r, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.bounded(w, r, mux.ServeHTTP) }))
	})
}

// NativeHandlerForTest marks OAuth requests as native in isolated tests. Normal
// startup installs the issuer from the required persistent signing Secret.
func (s *Server) NativeHandlerForTest(secret []byte) http.Handler {
	if len(secret) != 32 {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "native fixture unavailable", http.StatusServiceUnavailable)
		})
	}
	if err := s.EnableNativeOAuth(secret); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "native fixture unavailable", http.StatusServiceUnavailable)
		})
	}
	if s.grantRevoked == nil {
		s.grantRevoked = func(string) error { return nil }
	}
	if s.grantRevocationFallback == nil {
		s.grantRevocationFallback = func(string) error { return nil }
	}
	if s.grantRevocationComplete == nil {
		s.grantRevocationComplete = func(string) error { return nil }
	}
	if s.accountInvalidated == nil {
		s.accountInvalidated = func(string, string) error { return nil }
	}
	if s.accountInvalidationFallback == nil {
		s.accountInvalidationFallback = func(string, string) error { return nil }
	}
	if s.accountInvalidationComplete == nil {
		s.accountInvalidationComplete = func(string) error { return nil }
	}
	base := s.Handler()
	return base
}

// EnableNativeOAuth installs the Fosite-only issuer after startup verifies the
// signing key against the completed durable reset state.
func (s *Server) EnableNativeOAuth(secret []byte) error {
	if len(secret) != 32 {
		return errors.New("native OAuth signing key must contain 32 bytes")
	}
	s.nativeIssuer = issuer.NewProvider(secret, issuer.Redirects{
		issuer.Electron:  "http://127.0.0.1/oauth/callback",
		issuer.Android:   s.AndroidRedirect,
		issuer.Extension: s.ExtensionRedirect,
	}, nil)
	return nil
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	s.loginFlow(w, r, nil)
}
func (s *Server) loginFlow(w http.ResponseWriter, r *http.Request, intent *oauthIntent) {
	select {
	case s.providerSlots <- struct{}{}:
		defer func() { <-s.providerSlots }()
	default:
		markDiagnostic(r, "refused", "rate_limited")
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
	s.mu.Lock()
	now := time.Now()
	for k, f := range s.flows {
		if !now.Before(f.expires) {
			delete(s.flows, k)
		}
	}
	for k, consent := range s.consents {
		if !now.Before(consent.expires) {
			delete(s.consents, k)
		}
	}
	if len(s.flows)+len(s.consents) >= 1024 {
		s.mu.Unlock()
		markDiagnostic(r, "refused", "rate_limited")
		fail(w, 503)
		return
	}
	s.flowSeq++
	s.flows[state] = flow{state: state, nonce: nonce, binding: binding, expires: now.Add(10 * time.Minute), intent: intent, seq: s.flowSeq, epoch: s.flowEpoch}
	s.mu.Unlock()
	cookie(w, bindingCookie, binding, 600)
	authorizationURL := s.googleOAuth.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce))
	http.Redirect(w, r, authorizationURL, http.StatusFound)
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
		markDiagnostic(r, "refused", "rate_limited")
		fail(w, 503)
		return
	}
	s.mu.Lock()
	f, ok := s.flows[state]
	if ok && !time.Now().Before(f.expires) {
		delete(s.flows, state)
		ok = false
	}
	if ok && subtle.ConstantTimeCompare([]byte(bind.Value), []byte(f.binding)) == 1 {
		delete(s.flows, state)
	} else {
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		fail(w, 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	providerCtx, cancelProvider := context.WithTimeout(ctx, 8*time.Second)
	claims, e := s.exchange(providerCtx, code, f.nonce)
	cancelProvider()
	if e != nil {
		if errors.Is(e, errProviderUnavailable) {
			markDiagnostic(r, "failed", "provider_unavailable")
		} else {
			markDiagnostic(r, "refused", "verification_failed")
		}
		fail(w, 401)
		return
	}
	if !time.Now().Before(f.expires) {
		fail(w, 401)
		return
	}
	key := s.identityKey(claims.Subject)
	op, e := s.accountRegistration.StartAccountRegistration(ctx)
	if e != nil {
		fail(w, 503)
		return
	}
	defer op.Close()
	ctx = op.Context()
	guard := s.identityGuard(key)
	if !lockIdentity(ctx, guard) {
		fail(w, 503)
		return
	}
	guardHeld := true
	defer func() {
		if guardHeld {
			guard.Unlock()
		}
	}()
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
	tx, e := beginAccountTransaction(op)
	if e != nil {
		fail(w, 503)
		return
	}
	defer tx.Rollback(ctx)
	registered, e := tx.UpsertRelayAccount(ctx, persistence.AccountRegistration{
		ID: account, Issuer: googleIssuer, Subject: claims.Subject, Email: claims.Email,
		EmailVerified: claims.EmailVerified, HostedDomain: claims.HostedDomain,
	})
	if e != nil {
		fail(w, 503)
		return
	}
	accountID, generation := registered.ID, registered.Generation
	if pending, uncertain := s.uncertainAccounts.Load(accountID); uncertain {
		var deletionOperationID string
		if expected, ok := pending.(uncertainDeletion); ok {
			deletionOperationID = expected.operationID
		}
		state, stateErr := tx.AccountStateAfterUncertain(ctx, accountID, deletionOperationID)
		if stateErr != nil {
			fail(w, 503)
			return
		}
		switch expected := pending.(type) {
		case uncertainMutation:
			if state.Revision != expected.revision || state.Generation != expected.generation || state.Status != expected.status {
				fail(w, 503)
				return
			}
		case uncertainDeletion:
			if state.DeletionStarted || state.DeletionOperationPresent {
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
	if registered.Created {
		if e = s.importRetainedUsage(ctx, tx, accountID, claims.Subject); e != nil {
			fail(w, 503)
			return
		}
		if e = tx.RecordAccountCreated(ctx, auditID, accountID); e != nil {
			fail(w, 503)
			return
		}
	}
	csrf := s.csrf(s.CurrentPepper, credential)
	if e = tx.CreatePortalSession(ctx, digest32(s.digest(s.CurrentPepper, "portal-session", credential)), s.CurrentPepper, accountID, generation, digest32(s.digest(s.CurrentPepper, "csrf-store", csrf))); e != nil {
		fail(w, 503)
		return
	}
	if e = s.commitPortalTransaction(ctx, tx); e != nil {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		fail(w, 503)
		return
	}
	markDiagnostic(r, "success", "none")
	cookie(w, sessionCookie, credential, 0)
	if f.intent != nil {
		guard.Unlock()
		guardHeld = false
		op.Close()
		s.presentConsent(w, r, accountID, credential, s.CurrentPepper, claims.Subject, claims.Email, *f.intent)
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

var errProviderUnavailable = errors.New("provider unavailable")

func (s *Server) exchange(ctx context.Context, code, nonce string) (result identity, resultErr error) {
	started := time.Now()
	tokenOK := false
	var classificationErr error
	defer func() {
		if tokenOK {
			s.transitions.Dependency("dependency.google_token", nil, ctx, time.Since(started))
		} else {
			if classificationErr == nil {
				classificationErr = resultErr
			}
			s.transitions.Dependency("dependency.google_token", classificationErr, ctx, time.Since(started))
		}
	}()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, s.client)
	providerToken, err := s.googleOAuth.Exchange(ctx, code)
	if err != nil {
		return identity{}, errProviderUnavailable
	}
	tokenOK = true
	rawIDToken, ok := providerToken.Extra("id_token").(string)
	if !ok || len(rawIDToken) == 0 || len(rawIDToken) > providerBodyLimit {
		classificationErr = diagnostic.ErrInvalidResponse
		return identity{}, errProviderUnavailable
	}
	return s.verify(ctx, rawIDToken, nonce)
}
func (s *Server) verify(ctx context.Context, raw, nonce string) (identity, error) {
	if len(raw) == 0 || len(raw) > providerBodyLimit {
		return identity{}, errors.New("token size")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return identity{}, errors.New("token format")
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(header) > 4096 {
		return identity{}, errors.New("token header size")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > 16384 {
		return identity{}, errors.New("token payload size")
	}

	verified, err := s.googleVerifier.Verify(oidc.ClientContext(ctx, s.client), raw)
	if err != nil {
		return identity{}, errors.New("token verification failed")
	}
	var claims struct {
		Issuer        string          `json:"iss"`
		Subject       string          `json:"sub"`
		Audience      json.RawMessage `json:"aud"`
		Email         string          `json:"email"`
		EmailVerified bool            `json:"email_verified"`
		HostedDomain  *string         `json:"hd"`
		Nonce         string          `json:"nonce"`
		IssuedAt      int64           `json:"iat"`
	}
	if err := verified.Claims(&claims); err != nil {
		return identity{}, errors.New("token claims invalid")
	}
	var audience string
	if len(claims.Audience) == 0 || claims.Audience[0] != '"' || json.Unmarshal(claims.Audience, &audience) != nil || audience != s.ClientID {
		return identity{}, errors.New("token audience invalid")
	}
	now := time.Now()
	if claims.Issuer != "accounts.google.com" && claims.Issuer != googleIssuer {
		return identity{}, errors.New("token issuer invalid")
	}
	if verified.Expiry.Unix() <= now.Unix() || time.Unix(claims.IssuedAt, 0).After(now.Add(time.Minute)) || claims.Nonce != nonce || claims.Subject == "" || len(claims.Subject) > 255 || claims.Email == "" || len(claims.Email) > 320 {
		return identity{}, errors.New("token claims invalid")
	}
	return identity{
		Issuer:        googleIssuer,
		Subject:       claims.Subject,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		HostedDomain:  claims.HostedDomain,
		Audience:      audience,
		Nonce:         claims.Nonce,
		Expiry:        verified.Expiry.Unix(),
		IssuedAt:      claims.IssuedAt,
	}, nil
}
func (s *Server) session(r *http.Request) (string, uint64, string, string, string, error) {
	c, e := r.Cookie(sessionCookie)
	if e != nil || len(c.Value) != 43 {
		return "", 0, "", "", "", errInvalidSession
	}
	op, e := s.portalSessions.StartPortalSession(r.Context())
	if e != nil {
		return "", 0, "", "", "", e
	}
	defer op.Close()
	tx, e := op.BeginPortalSession(op.Context())
	if e != nil {
		return "", 0, "", "", "", e
	}
	finished := false
	defer func() {
		if !finished {
			_ = tx.Rollback(op.Context())
		}
	}()
	for version := range s.Peppers {
		digest := digest32(s.digest(version, "portal-session", c.Value))
		stored, err := tx.PortalSessionForOwner(op.Context(), digest, version)
		if errors.Is(err, persistence.ErrInvalidCredential) {
			continue
		}
		if err != nil {
			return "", 0, "", "", "", err
		}
		if _, uncertain := s.uncertainAccounts.Load(stored.AccountID); uncertain {
			return "", 0, "", "", "", errInvalidSession
		}
		if !s.allowAccount(stored.Subject) {
			return "", 0, "", "", "", errRateLimit
		}
		token := s.csrf(stored.PepperVersion, c.Value)
		if subtle.ConstantTimeCompare(stored.CSRFHash, s.digest(stored.PepperVersion, "csrf-store", token)) != 1 {
			return "", 0, "", "", "", errors.New("csrf")
		}
		touched, err := tx.TouchPortalSession(op.Context(), digest, version, stored.AccountID, stored.Generation)
		if err != nil {
			return "", 0, "", "", "", err
		}
		if !touched {
			return "", 0, "", "", "", errInvalidSession
		}
		if err = tx.Commit(op.Context()); err != nil {
			finished = true
			return "", 0, "", "", "", err
		}
		finished = true
		return stored.AccountID, version, stored.Status, stored.Email, token, nil
	}
	if e = tx.Rollback(op.Context()); e != nil {
		finished = true
		return "", 0, "", "", "", e
	}
	finished = true
	return "", 0, "", "", "", errInvalidSession
}

type quotaHistory = portal.QuotaHistoryEntry

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/account/security" {
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
	var recoveryHeld bool
	var weekStart time.Time
	var weeklyBytes *int64
	var sessions *int
	var verified bool
	var hd *string
	var recorded int64
	var historyJSON []byte
	if e == nil {
		if s.beforeProfileRead != nil {
			s.beforeProfileRead()
		}
		profile, err := s.ownerUsageProfile(r, id, version)
		weekStart = profile.WeekStart
		status, email, verified, hd = profile.Status, profile.Email, profile.Verified, profile.HostedDomain
		recoveryHeld = profile.RecoveryHeld
		weeklyBytes, sessions, recorded, historyJSON = profile.WeeklyBytes, profile.Sessions, profile.Usage, profile.HistoryJSON
		if errors.Is(err, persistence.ErrNotFound) {
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
	if recoveryHeld {
		description = "Recovery Review Hold: an administrator must review your status and quota assignments before relay use."
	}
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
	showAllHistory := r.URL.Query().Get("history") == "all"
	history = portal.QuotaHistoryRange(history, showAllHistory)
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
	if bytesValue > 0 {
		percent = min(100, int64(float64(recorded)*100/float64(bytesValue)))
	}
	usageState := "Remaining allowance: " + portal.FormatBytes(max(0, bytesValue-recorded)) + "."
	remaining := max(0, bytesValue-recorded)
	if bytesValue == 0 {
		usageState = "Zero allowance: relay use is unavailable."
	}
	if recorded > bytesValue {
		usageState += " Recorded Relay Usage exceeds the current allowance."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	relayURL, _ := url.Parse(s.Origin)
	pageData := struct {
		SignedIn                               bool
		Status, Description, Email, CSRF       string
		DiscoveryURL, RelayHost                string
		Active, Admin                          bool
		Security                               bool
		Notice                                 string
		RecoveryHeld                           bool
		WeekStart                              time.Time
		WeeklyBytes                            int64
		Sessions                               int
		Recorded, Percent                      int64
		Remaining                              int64
		UsageState                             string
		History                                []quotaHistory
		ShowAllHistory                         bool
		LiveSessions, ActiveGrants, GrantLimit int
		LiveSessionsKnown, ActiveGrantsKnown   bool
	}{e == nil, status, description, email, csrf, s.Origin + "/v1/relay", relayURL.Host, status == "Active" && !recoveryHeld, admin, r.URL.Path == "/account/security", r.URL.Query().Get("notice"), recoveryHeld, weekStart, bytesValue, sessionsValue, recorded, percent, remaining, usageState, history, showAllHistory, liveSessions, activeGrants, 20, liveKnown, grantsKnown}
	if pageData.Security && !pageData.SignedIn {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}
	if pageData.Security {
		portal.Render(w, http.StatusOK, "account-security", pageData)
		return
	}
	portal.Render(w, http.StatusOK, "account", pageData)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.Origin {
		markDiagnostic(r, "refused", "origin_rejected")
		fail(w, 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		markDiagnostic(r, "refused", "csrf_rejected")
		fail(w, 400)
		return
	}
	_, version, _, _, csrf, e := s.session(r)
	if errors.Is(e, errRateLimit) {
		fail(w, 429)
		return
	}
	if e != nil && !errors.Is(e, errInvalidSession) {
		markDiagnostic(r, "failed", "database_unavailable")
		fail(w, 503)
		return
	}
	if e != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(csrf)) != 1 {
		if e != nil {
			markDiagnostic(r, "refused", "invalid_credentials")
		} else {
			markDiagnostic(r, "refused", "csrf_rejected")
		}
		fail(w, 403)
		return
	}
	c, _ := r.Cookie(sessionCookie)
	op, e := s.portalSessions.StartPortalSession(r.Context())
	if e != nil {
		// The single-statement commit acknowledgement does not prove rollback.
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		fail(w, 503)
		return
	}
	defer op.Close()
	tx, e := op.BeginPortalSession(op.Context())
	if e != nil {
		markDiagnostic(r, "failed", "database_unavailable")
		fail(w, 503)
		return
	}
	defer tx.Rollback(op.Context())
	if _, e = tx.DeletePortalSession(op.Context(), digest32(s.digest(version, "portal-session", c.Value)), version); e != nil {
		markDiagnostic(r, "failed", "database_unavailable")
		fail(w, 503)
		return
	}
	if e = s.commitPortalTransaction(op.Context(), tx); e != nil {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		fail(w, 503)
		return
	}
	cookie(w, sessionCookie, "", -1)
	http.Redirect(w, r, "/?notice=portal-logout", http.StatusSeeOther)
}

func (s *Server) revokeOwner(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.Origin {
		markDiagnostic(r, "refused", "origin_rejected")
		fail(w, 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		markDiagnostic(r, "refused", "csrf_rejected")
		fail(w, 400)
		return
	}
	if r.PostForm.Get("confirm") != "yes" {
		markDiagnostic(r, "refused", "confirmation_required")
		http.Error(w, "Review the account-wide consequences and confirm before signing out everywhere.", http.StatusBadRequest)
		return
	}

	outcome := s.revokeAccountAccess(r)
	s.renderAccountRevocation(w, r, outcome)
}
