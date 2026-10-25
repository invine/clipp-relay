package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"clipp-relay/internal/auth/issuer"
	"clipp-relay/internal/portal"
	"github.com/jackc/pgx/v5"
	"github.com/ory/fosite"
)

type nativeOAuthContextKey struct{}

func isNativeOAuth(r *http.Request) bool {
	enabled, _ := r.Context().Value(nativeOAuthContextKey{}).(bool)
	return enabled
}

func (s *Server) commitCredentialTx(ctx context.Context, tx pgx.Tx) error {
	if s.commitCredential != nil {
		return s.commitCredential(ctx, tx)
	}
	return tx.Commit(ctx)
}

type oauthIntent struct{ client, redirect, state, challenge string }
type oauthConsent struct {
	intent           oauthIntent
	authorizeRequest fosite.AuthorizeRequester
	account          string
	generation       int64
	binding          []byte
	version          uint64
	expires          time.Time
	epoch            uint64
}

// Registered redirects are exact deployment inputs. Only Electron's loopback port varies.
func (s *Server) registeredRedirect(client, redirect string) bool {
	if len(redirect) > 512 || strings.ContainsAny(redirect, "#\r\n") {
		return false
	}
	u, e := url.Parse(redirect)
	if e != nil || u.User != nil || u.Fragment != "" || u.RawFragment != "" || u.RawQuery != "" || u.Opaque != "" || u.String() != redirect {
		return false
	}
	switch client {
	case "electron":
		host, port, e := net.SplitHostPort(u.Host)
		if e != nil || host != "127.0.0.1" || u.Scheme != "http" || u.Path != "/oauth/callback" || u.RawPath != "" {
			return false
		}
		n, e := strconv.Atoi(port)
		return e == nil && n >= 1024 && n <= 65535
	case "android":
		return s.AndroidRedirect != "" && redirect == s.AndroidRedirect
	case "extension":
		return s.ExtensionRedirect != "" && redirect == s.ExtensionRedirect
	default:
		return false
	}
}
func validPKCE(challenge string) bool {
	if len(challenge) != 43 {
		return false
	}
	b, e := base64.RawURLEncoding.DecodeString(challenge)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == challenge
}
func validVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~') {
			return false
		}
	}
	return true
}
func (s *Server) oauthError(w http.ResponseWriter, status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
}
func (s *Server) auditCredential(ctx context.Context, tx pgx.Tx, event, account, client string) error {
	id, e := uuid()
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id,client_type) VALUES($1,clock_timestamp(),$2,$3,$4)`, id, event, account, client)
	return e
}
func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if len(q) != 6 {
		s.oauthError(w, 400)
		return
	}
	for _, key := range []string{"response_type", "client_id", "redirect_uri", "code_challenge_method", "code_challenge", "state"} {
		if len(q[key]) != 1 {
			s.oauthError(w, 400)
			return
		}
	}
	client, redirect, state, challenge := q.Get("client_id"), q.Get("redirect_uri"), q.Get("state"), q.Get("code_challenge")
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || !s.registeredRedirect(client, redirect) || !validPKCE(challenge) || len(state) < 16 || len(state) > 512 || strings.ContainsAny(state, "\r\n") {
		s.oauthError(w, 400)
		return
	}
	if isNativeOAuth(r) {
		if _, err := s.nativeAuthorizeRequest(r.Context(), oauthIntent{client, redirect, state, challenge}); err != nil {
			s.oauthError(w, 400)
			return
		}
	}
	markClient(r, client)
	account, version, _, _, _, e := s.session(r)
	if e != nil {
		if errors.Is(e, errInvalidSession) {
			s.loginFlow(w, r, &oauthIntent{client, redirect, state, challenge})
		} else {
			s.oauthError(w, 503)
		}
		return
	}
	var subject string
	if e = s.db.QueryRow(r.Context(), `SELECT subject FROM public.accounts WHERE id=$1`, account).Scan(&subject); e != nil {
		s.oauthError(w, 503)
		return
	}
	cookie, _ := r.Cookie(sessionCookie)
	s.presentConsent(w, r, account, cookie.Value, version, subject, oauthIntent{client, redirect, state, challenge})
}

func (s *Server) nativeAuthorizeRequest(ctx context.Context, intent oauthIntent) (fosite.AuthorizeRequester, error) {
	if s.nativeIssuer == nil {
		return nil, errors.New("native issuer is not enabled in this fixture")
	}
	q := url.Values{"response_type": {"code"}, "client_id": {intent.client}, "redirect_uri": {intent.redirect}, "code_challenge_method": {"S256"}, "code_challenge": {intent.challenge}, "state": {intent.state}}
	r := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil).WithContext(ctx)
	return s.nativeIssuer.NewAuthorizeRequest(ctx, r)
}
func denyAuthorization(w http.ResponseWriter, r *http.Request, intent oauthIntent) {
	target, _ := url.Parse(intent.redirect)
	params := target.Query()
	params.Set("error", "access_denied")
	params.Set("state", intent.state)
	target.RawQuery = params.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) presentConsent(w http.ResponseWriter, r *http.Request, account, credential string, version uint64, subject string, intent oauthIntent) {
	var authorizeRequest fosite.AuthorizeRequester
	var grantID string
	if isNativeOAuth(r) {
		var err error
		authorizeRequest, err = s.nativeAuthorizeRequest(r.Context(), intent)
		if err != nil {
			s.oauthError(w, 400)
			return
		}
		grantID, err = uuid()
		if err != nil {
			s.oauthError(w, 503)
			return
		}
		authorizeRequest.SetID(grantID)
	}
	id, e := opaque()
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	unitCtx, releaseUnit, e := s.db.StartUnit(r.Context())
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	unitHeld := true
	defer func() {
		if unitHeld {
			releaseUnit()
		}
	}()
	guard := s.identityGuard(s.identityKey(subject))
	if !lockIdentity(unitCtx, guard) {
		s.oauthError(w, 503)
		return
	}
	guardHeld := true
	defer func() {
		if guardHeld {
			guard.Unlock()
		}
	}()
	binding := s.digest(version, "consent-browser", credential)
	tx, e := s.db.Begin(unitCtx)
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	defer tx.Rollback(unitCtx)
	var status string
	var generation int64
	if e = tx.QueryRow(unitCtx, `SELECT status,credential_generation FROM public.accounts WHERE id=$1 AND NOT recovery_review_hold FOR UPDATE`, account).Scan(&status, &generation); e != nil {
		s.oauthError(w, 503)
		return
	}
	var sessionGeneration int64
	var storedCSRF []byte
	if e = tx.QueryRow(unitCtx, `SELECT credential_generation,csrf_digest FROM public.portal_sessions WHERE credential_digest=$1 AND pepper_version=$2 AND account_id=$3 AND credential_generation=$4 AND idle_expires_at>clock_timestamp() AND absolute_expires_at>clock_timestamp() FOR UPDATE`, s.digest(version, "portal-session", credential), version, account, generation).Scan(&sessionGeneration, &storedCSRF); e != nil {
		if !errors.Is(e, pgx.ErrNoRows) {
			s.oauthError(w, 503)
			return
		}
		s.oauthError(w, 401)
		return
	}
	if sessionGeneration != generation || subtle.ConstantTimeCompare(storedCSRF, s.digest(version, "csrf-store", s.csrf(version, credential))) != 1 {
		s.oauthError(w, 401)
		return
	}
	if status != "Active" {
		if s.auditCredential(unitCtx, tx, "credential_blocked", account, intent.client) != nil {
			s.oauthError(w, 503)
			return
		}
		if tx.Commit(unitCtx) != nil {
			if trace := diagnosticFor(r); trace != nil && trace.event == "oauth.authorize" {
				markDiagnostic(r, "uncertain", "commit_unconfirmed")
			}
			s.oauthError(w, 503)
			return
		}
		if trace := diagnosticFor(r); trace != nil && trace.event == "oauth.authorize" {
			markDiagnostic(r, "refused", "account_blocked")
		}
		denyAuthorization(w, r, intent)
		return
	}
	if tx.Commit(unitCtx) != nil {
		if trace := diagnosticFor(r); trace != nil && trace.event == "oauth.authorize" {
			markDiagnostic(r, "uncertain", "commit_unconfirmed")
		}
		s.oauthError(w, 503)
		return
	}
	s.mu.Lock()
	now := time.Now()
	for key, flow := range s.flows {
		if !now.Before(flow.expires) {
			delete(s.flows, key)
		}
	}
	for key, consent := range s.consents {
		if !now.Before(consent.expires) {
			delete(s.consents, key)
		}
	}
	if len(s.flows)+len(s.consents) >= 1024 {
		s.mu.Unlock()
		if trace := diagnosticFor(r); trace != nil && trace.event == "oauth.authorize" {
			markDiagnostic(r, "refused", "rate_limited")
		}
		s.oauthError(w, 503)
		return
	}
	if authorizeRequest != nil {
		authorizeRequest.SetSession(issuer.NewSession(issuer.Principal{AccountID: account, Generation: generation, Client: issuer.ClientType(intent.client), GrantID: grantID}))
	}
	s.consents[id] = oauthConsent{intent: intent, authorizeRequest: authorizeRequest, account: account, generation: generation, binding: binding, version: version, expires: now.Add(10 * time.Minute), epoch: s.flowEpoch}
	s.mu.Unlock()
	guard.Unlock()
	guardHeld = false
	releaseUnit()
	unitHeld = false
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Browsers also apply form-action to the post-consent redirect. The app
	// callback was validated against its registered redirect before this flow.
	// URI delimiters must not become CSP directive or policy-list separators.
	callbackSource := strings.NewReplacer(";", "%3B", ",", "%2C").Replace(intent.redirect)
	if intent.client == "android" {
		// Chromium does not match private application URIs as host sources.
		// Permit the registered scheme in CSP; the OAuth redirect itself stays exact.
		callback, _ := url.Parse(intent.redirect)
		callbackSource = callback.Scheme + ":"
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self' "+callbackSource+"; base-uri 'none'")
	portal.Render(w, http.StatusOK, "consent", struct{ Client, Flow, CSRF string }{intent.client, id, s.csrf(version, credential)})
}
func (s *Server) confirmAuthorization(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != s.Origin || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		s.oauthError(w, 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil || len(r.PostForm) != 2 || len(r.PostForm["flow"]) != 1 || len(r.PostForm["csrf"]) != 1 {
		s.oauthError(w, 400)
		return
	}
	account, version, _, _, csrf, e := s.session(r)
	if e != nil {
		s.oauthError(w, 401)
		return
	}
	if subtle.ConstantTimeCompare([]byte(csrf), []byte(r.PostForm.Get("csrf"))) != 1 {
		s.oauthError(w, 403)
		return
	}
	cookie, e := r.Cookie(sessionCookie)
	if e != nil {
		s.oauthError(w, 401)
		return
	}
	var subject string
	if e = s.db.QueryRow(r.Context(), `SELECT subject FROM public.accounts WHERE id=$1`, account).Scan(&subject); e != nil {
		s.oauthError(w, 401)
		return
	}
	unitCtx, releaseUnit, e := s.db.StartUnit(r.Context())
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	defer releaseUnit()
	r = r.WithContext(unitCtx)
	guard := s.identityGuard(s.identityKey(subject))
	if !lockIdentity(unitCtx, guard) {
		s.oauthError(w, 503)
		return
	}
	defer guard.Unlock()
	id := r.PostForm.Get("flow")
	s.mu.Lock()
	consent, ok := s.consents[id]
	if ok {
		delete(s.consents, id)
	}
	epoch := s.flowEpoch
	s.mu.Unlock()
	if ok {
		markClient(r, consent.intent.client)
	}
	if ok && time.Now().After(consent.expires) {
		markDiagnostic(r, "refused", "expired_credentials")
	}
	if !ok || time.Now().After(consent.expires) || consent.epoch != epoch || consent.account != account || consent.version != version || subtle.ConstantTimeCompare(consent.binding, s.digest(version, "consent-browser", cookie.Value)) != 1 {
		s.oauthError(w, 401)
		return
	}
	if consent.authorizeRequest != nil && isNativeOAuth(r) {
		s.issueNativeCode(w, r, consent.authorizeRequest, account, consent.generation, consent.intent)
		return
	}
	s.issueCode(w, r, account, consent.generation, consent.intent)
}
func (s *Server) issueCode(w http.ResponseWriter, r *http.Request, account string, expectedGeneration int64, intent oauthIntent) {
	code, e := opaque()
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	defer tx.Rollback(r.Context())
	var status string
	var generation int64
	e = tx.QueryRow(r.Context(), `SELECT status,credential_generation FROM public.accounts WHERE id=$1 AND NOT recovery_review_hold FOR UPDATE`, account).Scan(&status, &generation)
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	if generation != expectedGeneration {
		s.oauthError(w, 401)
		return
	}
	if status != "Active" {
		if s.auditCredential(r.Context(), tx, "credential_blocked", account, intent.client) != nil {
			s.oauthError(w, 503)
			return
		}
		if tx.Commit(r.Context()) != nil {
			markDiagnostic(r, "uncertain", "commit_unconfirmed")
			s.oauthError(w, 503)
			return
		}
		markDiagnostic(r, "refused", "account_blocked")
		denyAuthorization(w, r, intent)
		return
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO public.authorization_codes(credential_digest,pepper_version,account_id,credential_generation,client_type,redirect_uri,challenge,issued_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp(),clock_timestamp()+interval '5 minutes')`, s.digest(s.CurrentPepper, "authorization-code", code), s.CurrentPepper, account, generation, intent.client, intent.redirect, intent.challenge)
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	if s.commitCredentialTx(r.Context(), tx) != nil {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		s.oauthError(w, 503)
		return
	}
	target, _ := url.Parse(intent.redirect)
	params := target.Query()
	params.Set("code", code)
	params.Set("state", intent.state)
	target.RawQuery = params.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) issueNativeCode(w http.ResponseWriter, r *http.Request, authorize fosite.AuthorizeRequester, account string, expectedGeneration int64, intent oauthIntent) {
	unitCtx := r.Context()
	var err error
	var subject string
	if err = s.Pool.QueryRow(unitCtx, `SELECT subject FROM public.accounts WHERE id=$1`, account).Scan(&subject); err != nil {
		s.oauthError(w, 401)
		return
	}
	tx, err := s.db.Begin(unitCtx)
	if err != nil {
		s.oauthError(w, 503)
		return
	}
	defer tx.Rollback(unitCtx)
	var status string
	var generation int64
	if err = tx.QueryRow(unitCtx, `SELECT status,credential_generation FROM public.accounts WHERE id=$1 AND NOT recovery_review_hold FOR UPDATE`, account).Scan(&status, &generation); err != nil {
		s.oauthError(w, 503)
		return
	}
	if generation != expectedGeneration {
		s.oauthError(w, 401)
		return
	}
	if status != "Active" {
		if s.auditCredential(unitCtx, tx, "credential_blocked", account, intent.client) != nil || s.commitCredentialTx(unitCtx, tx) != nil {
			s.oauthError(w, 503)
			return
		}
		denyAuthorization(w, r, intent)
		return
	}
	ctx := issuer.InTransaction(unitCtx, tx, issuer.ClientType(intent.client), "")
	response, err := s.nativeIssuer.NewAuthorizeResponse(ctx, authorize, authorize.GetSession())
	if err != nil {
		s.oauthError(w, 400)
		return
	}
	if err = s.commitCredentialTx(unitCtx, tx); err != nil {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		s.oauthError(w, 503)
		return
	}
	target, err := url.Parse(intent.redirect)
	if err != nil {
		s.oauthError(w, 503)
		return
	}
	query := target.Query()
	for key, values := range response.GetParameters() {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	target.RawQuery = query.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target.String(), http.StatusFound)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) lockCredentialAccount(ctx context.Context, tx pgx.Tx, account string) (string, int64, error) {
	var status, subject string
	var generation int64
	if e := tx.QueryRow(ctx, `SELECT status,credential_generation,subject FROM public.accounts WHERE id=$1 AND NOT recovery_review_hold FOR UPDATE`, account).Scan(&status, &generation, &subject); e != nil {
		return "", 0, e
	}
	if !s.allowAccount(subject) {
		return "", 0, errRateLimit
	}
	return status, generation, nil
}
func (s *Server) insertCredentialTokens(ctx context.Context, tx pgx.Tx, grant, refresh, access string, refreshGeneration int64) error {
	_, e := tx.Exec(ctx, `INSERT INTO public.refresh_generations(credential_digest,pepper_version,grant_id,generation,issued_at) VALUES($1,$2,$3,$4,clock_timestamp())`, s.digest(s.CurrentPepper, "refresh-token", refresh), s.CurrentPepper, grant, refreshGeneration)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO public.relay_access_tokens(credential_digest,pepper_version,grant_id,issued_at,expires_at) VALUES($1,$2,$3,clock_timestamp(),clock_timestamp()+interval '15 minutes')`, s.digest(s.CurrentPepper, "relay-access", access), s.CurrentPepper, grant)
	return e
}

func writeTokens(w http.ResponseWriter, access, refresh string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tokenResponse{access, "Bearer", 900, refresh})
}
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if isNativeOAuth(r) {
		s.nativeToken(w, r)
		return
	}
	if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("Content-Encoding") != "" || r.URL.RawQuery != "" {
		s.oauthError(w, 400)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil {
		s.oauthError(w, 400)
		return
	}
	client := r.PostForm.Get("client_id")
	if trace := diagnosticFor(r); trace != nil && r.PostForm.Get("grant_type") == "refresh_token" {
		trace.event = "oauth.refresh"
	}
	allowed := map[string]bool{"grant_type": true, "client_id": true}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		allowed["code"] = true
		allowed["redirect_uri"] = true
		allowed["code_verifier"] = true
	case "refresh_token":
		allowed["refresh_token"] = true
	}
	for key, values := range r.PostForm {
		if !allowed[key] || len(values) != 1 {
			s.oauthError(w, 400)
			return
		}
	}
	for key := range allowed {
		if len(r.PostForm[key]) != 1 {
			s.oauthError(w, 400)
			return
		}
	}
	if client != "electron" && client != "android" && client != "extension" {
		s.oauthError(w, 400)
		return
	}
	markClient(r, client)
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.exchangeCode(w, r, client)
	case "refresh_token":
		s.refresh(w, r, client)
	default:
		s.oauthError(w, 400)
	}
}
func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request, client string) {
	code, redirect, verifier := r.PostForm.Get("code"), r.PostForm.Get("redirect_uri"), r.PostForm.Get("code_verifier")
	if len(code) != 43 || !s.registeredRedirect(client, redirect) || !validVerifier(verifier) {
		s.oauthError(w, 400)
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	var account string
	var version uint64
	var digest []byte
	for v := range s.Peppers {
		d := s.digest(v, "authorization-code", code)
		e := s.db.QueryRow(r.Context(), `SELECT account_id FROM public.authorization_codes WHERE credential_digest=$1 AND pepper_version=$2`, d, v).Scan(&account)
		if e == nil {
			version = v
			digest = d
			break
		}
		if e != pgx.ErrNoRows {
			s.oauthError(w, 503)
			return
		}
	}
	if account == "" {
		s.oauthError(w, 401)
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	defer tx.Rollback(r.Context())
	status, generation, e := s.lockCredentialAccount(r.Context(), tx, account)
	if e != nil {
		if errors.Is(e, errRateLimit) {
			s.oauthError(w, 429)
		} else {
			s.oauthError(w, 503)
		}
		return
	}
	var codeGeneration int64
	var storedClient, storedRedirect, storedChallenge string
	var fresh bool
	e = tx.QueryRow(r.Context(), `SELECT credential_generation,client_type,redirect_uri,challenge,consumed_at IS NULL AND expires_at>clock_timestamp() FROM public.authorization_codes WHERE credential_digest=$1 AND pepper_version=$2 FOR UPDATE`, digest, version).Scan(&codeGeneration, &storedClient, &storedRedirect, &storedChallenge, &fresh)
	if e == nil && !fresh {
		markDiagnostic(r, "refused", "expired_credentials")
	}
	if e != nil || !fresh || storedClient != client || storedRedirect != redirect || subtle.ConstantTimeCompare([]byte(storedChallenge), []byte(challenge)) != 1 || codeGeneration != generation {
		s.oauthError(w, 401)
		return
	}
	if status != "Active" {
		if s.auditCredential(r.Context(), tx, "credential_blocked", account, client) != nil {
			s.oauthError(w, 503)
			return
		}
		if tx.Commit(r.Context()) != nil {
			markDiagnostic(r, "uncertain", "commit_unconfirmed")
			s.oauthError(w, 503)
			return
		}
		markDiagnostic(r, "refused", "account_blocked")
		s.oauthError(w, 403)
		return
	}
	var count int
	if e = tx.QueryRow(r.Context(), `SELECT count(*) FROM public.login_grants WHERE account_id=$1 AND credential_generation=$2 AND terminated_at IS NULL AND idle_expires_at>clock_timestamp() AND absolute_expires_at>clock_timestamp()`, account, generation).Scan(&count); e != nil {
		s.oauthError(w, 503)
		return
	}
	if count >= 20 {
		if s.auditCredential(r.Context(), tx, "grant_cap", account, client) != nil {
			s.oauthError(w, 503)
			return
		}
		if tx.Commit(r.Context()) != nil {
			markDiagnostic(r, "uncertain", "commit_unconfirmed")
			s.oauthError(w, 503)
			return
		}
		markDiagnostic(r, "refused", "grant_limit")
		s.oauthError(w, 429)
		return
	}
	grant, e := uuid()
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	access, e := opaque()
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	refresh, e := opaque()
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	_, e = tx.Exec(r.Context(), `UPDATE public.authorization_codes SET consumed_at=clock_timestamp() WHERE credential_digest=$1`, digest)
	if e == nil {
		_, e = tx.Exec(r.Context(), `INSERT INTO public.login_grants(id,account_id,credential_generation,client_type,created_at,last_used_at,idle_expires_at,absolute_expires_at) VALUES($1,$2,$3,$4,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 days',clock_timestamp()+interval '180 days')`, grant, account, generation, client)
	}
	if e == nil {
		e = s.insertCredentialTokens(r.Context(), tx, grant, refresh, access, 1)
	}
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	if s.commitCredentialTx(r.Context(), tx) != nil {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		s.oauthError(w, 503)
		return
	}
	writeTokens(w, access, refresh)
}
func (s *Server) refresh(w http.ResponseWriter, r *http.Request, client string) {
	raw := r.PostForm.Get("refresh_token")
	if len(raw) != 43 {
		s.oauthError(w, 400)
		return
	}
	var account string
	var version uint64
	var digest []byte
	for v := range s.Peppers {
		d := s.digest(v, "refresh-token", raw)
		e := s.db.QueryRow(r.Context(), `SELECT g.account_id FROM public.refresh_generations f JOIN public.login_grants g ON g.id=f.grant_id WHERE f.credential_digest=$1 AND f.pepper_version=$2`, d, v).Scan(&account)
		if e == nil {
			version = v
			digest = d
			break
		}
		if e != pgx.ErrNoRows {
			s.oauthError(w, 503)
			return
		}
	}
	if account == "" {
		s.oauthError(w, 401)
		return
	}
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	defer tx.Rollback(r.Context())
	status, generation, e := s.lockCredentialAccount(r.Context(), tx, account)
	if e != nil {
		if errors.Is(e, errRateLimit) {
			s.oauthError(w, 429)
		} else {
			s.oauthError(w, 503)
		}
		return
	}
	var grant, storedClient string
	var grantGeneration, refreshGeneration, currentRefreshGeneration int64
	var active, consumed bool
	e = tx.QueryRow(r.Context(), `SELECT g.id,g.client_type,g.credential_generation,g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp(),f.consumed_at IS NOT NULL,f.generation,g.current_refresh_generation FROM public.refresh_generations f JOIN public.login_grants g ON g.id=f.grant_id WHERE f.credential_digest=$1 AND f.pepper_version=$2 FOR UPDATE OF g,f`, digest, version).Scan(&grant, &storedClient, &grantGeneration, &active, &consumed, &refreshGeneration, &currentRefreshGeneration)
	if e == nil && !active {
		markDiagnostic(r, "refused", "expired_credentials")
	}
	if e != nil || storedClient != client || !active || generation != grantGeneration {
		s.oauthError(w, 401)
		return
	}
	if consumed {
		_, e = tx.Exec(r.Context(), `UPDATE public.login_grants SET terminated_at=clock_timestamp() WHERE id=$1`, grant)
		if e == nil {
			e = s.auditCredential(r.Context(), tx, "refresh_reuse", account, client)
		}
		if e != nil {
			s.oauthError(w, 503)
			return
		}
		if tx.Commit(r.Context()) != nil {
			markDiagnostic(r, "uncertain", "commit_unconfirmed")
			s.oauthError(w, 503)
			return
		}
		markDiagnostic(r, "refused", "replay_detected")
		s.oauthError(w, 401)
		return
	}
	if refreshGeneration != currentRefreshGeneration {
		s.oauthError(w, 401)
		return
	}
	if status != "Active" {
		if s.auditCredential(r.Context(), tx, "credential_blocked", account, client) != nil {
			s.oauthError(w, 503)
			return
		}
		if tx.Commit(r.Context()) != nil {
			markDiagnostic(r, "uncertain", "commit_unconfirmed")
			s.oauthError(w, 503)
			return
		}
		markDiagnostic(r, "refused", "account_blocked")
		s.oauthError(w, 403)
		return
	}
	access, e := opaque()
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	next, e := opaque()
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	_, e = tx.Exec(r.Context(), `UPDATE public.refresh_generations SET consumed_at=clock_timestamp() WHERE credential_digest=$1`, digest)
	if e == nil {
		_, e = tx.Exec(r.Context(), `UPDATE public.login_grants SET last_used_at=clock_timestamp(),idle_expires_at=LEAST(absolute_expires_at,clock_timestamp()+interval '30 days'),current_refresh_generation=current_refresh_generation+1 WHERE id=$1`, grant)
	}
	if e == nil {
		e = s.insertCredentialTokens(r.Context(), tx, grant, next, access, currentRefreshGeneration+1)
	}
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	if s.commitCredentialTx(r.Context(), tx) != nil {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		s.oauthError(w, 503)
		return
	}
	writeTokens(w, access, next)
}

// AuthenticateAccess authorizes only local Discovery and Relay Authentication.
func (s *Server) AuthenticateAccess(ctx context.Context, raw string) (string, time.Time, error) {
	if s.nativeIssuer != nil {
		return s.authenticateNativeAccess(ctx, raw)
	}
	if len(raw) != 43 {
		return "", time.Time{}, errInvalidSession
	}
	for v := range s.Peppers {
		var account, status string
		var tokenGeneration, accountGeneration int64
		var until time.Time
		e := s.db.QueryRow(ctx, `SELECT g.account_id,a.status,g.credential_generation,a.credential_generation,t.expires_at FROM public.relay_access_tokens t JOIN public.login_grants g ON g.id=t.grant_id JOIN public.accounts a ON a.id=g.account_id WHERE NOT a.recovery_review_hold AND t.credential_digest=$1 AND t.pepper_version=$2 AND t.expires_at>clock_timestamp() AND g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp()`, s.digest(v, "relay-access", raw), v).Scan(&account, &status, &tokenGeneration, &accountGeneration, &until)
		if e == nil {
			if status != "Active" || tokenGeneration != accountGeneration {
				return "", time.Time{}, errInvalidSession
			}
			return account, until, nil
		}
		if e != pgx.ErrNoRows {
			return "", time.Time{}, e
		}
	}
	return "", time.Time{}, errInvalidSession
}
