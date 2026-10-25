package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
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

func (s *Server) nativeOAuthEnabled(_ *http.Request) bool { return s.nativeIssuer != nil }

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
	if !s.nativeOAuthEnabled(r) {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
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
	if s.nativeOAuthEnabled(r) {
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
	if !s.nativeOAuthEnabled(r) {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	var authorizeRequest fosite.AuthorizeRequester
	var grantID string
	if s.nativeOAuthEnabled(r) {
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
	if !s.nativeOAuthEnabled(r) {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
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
	if consent.authorizeRequest == nil {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	s.issueNativeCode(w, r, consent.authorizeRequest, account, consent.generation, consent.intent)
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

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if s.nativeIssuer == nil {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	s.nativeToken(w, r)
}

// AuthenticateAccess authorizes only local Discovery and Relay Authentication.
func (s *Server) AuthenticateAccess(ctx context.Context, raw string) (string, time.Time, error) {
	if s.nativeIssuer == nil {
		return "", time.Time{}, errInvalidSession
	}
	return s.authenticateNativeAccess(ctx, raw)
}
