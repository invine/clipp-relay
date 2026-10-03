package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type oauthIntent struct{ client, redirect, state, challenge string }
type oauthConsent struct {
	intent  oauthIntent
	account string
	binding []byte
	version uint64
	expires time.Time
	epoch   uint64
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
	account, _, _, _, _, e := s.session(r)
	if e != nil {
		if errors.Is(e, errInvalidSession) {
			s.loginFlow(w, r, &oauthIntent{client, redirect, state, challenge})
		} else {
			s.oauthError(w, 503)
		}
		return
	}
	cookie, _ := r.Cookie(sessionCookie)
	s.presentConsent(w, r, account, cookie.Value, oauthIntent{client, redirect, state, challenge})
}
func denyAuthorization(w http.ResponseWriter, r *http.Request, intent oauthIntent) {
	target, _ := url.Parse(intent.redirect)
	params := target.Query()
	params.Set("error", "access_denied")
	params.Set("state", intent.state)
	target.RawQuery = params.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) presentConsent(w http.ResponseWriter, r *http.Request, account, credential string, intent oauthIntent) {
	id, e := opaque()
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	nonce, e := opaque()
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	version := s.CurrentPepper
	binding := s.digest(version, "consent-browser", credential)
	s.mu.Lock()
	now := time.Now()
	for key, consent := range s.consents {
		if !now.Before(consent.expires) {
			delete(s.consents, key)
		}
	}
	if len(s.flows)+len(s.consents) >= 1024 {
		s.mu.Unlock()
		s.oauthError(w, 503)
		return
	}
	s.consents[id] = oauthConsent{intent: intent, account: account, binding: binding, version: version, expires: now.Add(10 * time.Minute), epoch: s.flowEpoch}
	s.mu.Unlock()
	tx, e := s.db.Begin(r.Context())
	if e != nil {
		s.dropConsent(id)
		s.oauthError(w, 503)
		return
	}
	defer tx.Rollback(r.Context())
	var status string
	var generation int64
	if e = tx.QueryRow(r.Context(), `SELECT status,credential_generation FROM public.accounts WHERE id=$1 FOR UPDATE`, account).Scan(&status, &generation); e != nil {
		s.dropConsent(id)
		s.oauthError(w, 503)
		return
	}
	if status != "Active" {
		s.dropConsent(id)
		if s.auditCredential(r.Context(), tx, "credential_blocked", account, intent.client) != nil || tx.Commit(r.Context()) != nil {
			s.oauthError(w, 503)
			return
		}
		denyAuthorization(w, r, intent)
		return
	}
	transaction, e := uuid()
	if e != nil {
		s.dropConsent(id)
		s.oauthError(w, 503)
		return
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO public.authorization_transactions(id,state_digest,nonce_digest,binding_digest,pepper_version,created_at,expires_at,flow_kind,client_type,redirect_uri,challenge,client_state_digest,account_id,credential_generation) VALUES($1,$2,$3,$4,$5,clock_timestamp(),clock_timestamp()+interval '10 minutes','clipp',$6,$7,$8,$9,$10,$11)`, transaction, s.digest(version, "consent-id", id), s.digest(version, "consent-nonce", nonce), binding, version, intent.client, intent.redirect, intent.challenge, s.digest(version, "client-state", intent.state), account, generation)
	if e != nil || tx.Commit(r.Context()) != nil {
		s.dropConsent(id)
		s.oauthError(w, 503)
		return
	}
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
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self' "+callbackSource+"; base-uri 'none'")
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8"><title>Authorize Clipp Relay</title><h1>Authorize Clipp Relay</h1><p>Allow the %s app to use this relay account?</p><form method="post" action="/oauth/authorize"><input type="hidden" name="flow" value="%s"><input type="hidden" name="csrf" value="%s"><button type="submit">Authorize</button></form></html>`, intent.client, id, s.csrf(version, credential))
}
func (s *Server) dropConsent(id string) { s.mu.Lock(); delete(s.consents, id); s.mu.Unlock() }
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
	if !ok || time.Now().After(consent.expires) || consent.epoch != epoch || consent.account != account || consent.version != version || subtle.ConstantTimeCompare(consent.binding, s.digest(version, "consent-browser", cookie.Value)) != 1 {
		s.oauthError(w, 401)
		return
	}
	var storedGeneration int64
	e = s.db.QueryRow(r.Context(), `UPDATE public.authorization_transactions SET claimed_at=clock_timestamp() WHERE state_digest=$1 AND binding_digest=$2 AND pepper_version=$3 AND flow_kind='clipp' AND account_id=$4 AND client_type=$5 AND redirect_uri=$6 AND challenge=$7 AND client_state_digest=$8 AND claimed_at IS NULL AND expires_at>clock_timestamp() RETURNING credential_generation`, s.digest(version, "consent-id", id), consent.binding, version, account, consent.intent.client, consent.intent.redirect, consent.intent.challenge, s.digest(version, "client-state", consent.intent.state)).Scan(&storedGeneration)
	if e != nil {
		s.oauthError(w, 401)
		return
	}
	s.issueCode(w, r, account, storedGeneration, consent.intent)
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
	e = tx.QueryRow(r.Context(), `SELECT status,credential_generation FROM public.accounts WHERE id=$1 FOR UPDATE`, account).Scan(&status, &generation)
	if e != nil {
		s.oauthError(w, 503)
		return
	}
	if generation != expectedGeneration {
		s.oauthError(w, 401)
		return
	}
	if status != "Active" {
		if s.auditCredential(r.Context(), tx, "credential_blocked", account, intent.client) != nil || tx.Commit(r.Context()) != nil {
			s.oauthError(w, 503)
			return
		}
		denyAuthorization(w, r, intent)
		return
	}
	_, e = tx.Exec(r.Context(), `INSERT INTO public.authorization_codes(credential_digest,pepper_version,account_id,credential_generation,client_type,redirect_uri,challenge,issued_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp(),clock_timestamp()+interval '5 minutes')`, s.digest(s.CurrentPepper, "authorization-code", code), s.CurrentPepper, account, generation, intent.client, intent.redirect, intent.challenge)
	if e != nil || tx.Commit(r.Context()) != nil {
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

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) lockCredentialAccount(ctx context.Context, tx pgx.Tx, account string) (string, int64, error) {
	var status, subject string
	var generation int64
	if e := tx.QueryRow(ctx, `SELECT status,credential_generation,subject FROM public.accounts WHERE id=$1 FOR UPDATE`, account).Scan(&status, &generation, &subject); e != nil {
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
	if e != nil || !fresh || storedClient != client || storedRedirect != redirect || subtle.ConstantTimeCompare([]byte(storedChallenge), []byte(challenge)) != 1 || codeGeneration != generation {
		s.oauthError(w, 401)
		return
	}
	if status != "Active" {
		if s.auditCredential(r.Context(), tx, "credential_blocked", account, client) != nil || tx.Commit(r.Context()) != nil {
			s.oauthError(w, 503)
			return
		}
		s.oauthError(w, 403)
		return
	}
	var count int
	if e = tx.QueryRow(r.Context(), `SELECT count(*) FROM public.login_grants WHERE account_id=$1 AND credential_generation=$2 AND terminated_at IS NULL AND idle_expires_at>clock_timestamp() AND absolute_expires_at>clock_timestamp()`, account, generation).Scan(&count); e != nil {
		s.oauthError(w, 503)
		return
	}
	if count >= 20 {
		if s.auditCredential(r.Context(), tx, "grant_cap", account, client) != nil || tx.Commit(r.Context()) != nil {
			s.oauthError(w, 503)
			return
		}
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
	if e != nil || tx.Commit(r.Context()) != nil {
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
	if e != nil || storedClient != client || !active || generation != grantGeneration {
		s.oauthError(w, 401)
		return
	}
	if consumed {
		_, e = tx.Exec(r.Context(), `UPDATE public.login_grants SET terminated_at=clock_timestamp() WHERE id=$1`, grant)
		if e == nil {
			e = s.auditCredential(r.Context(), tx, "refresh_reuse", account, client)
		}
		if e != nil || tx.Commit(r.Context()) != nil {
			s.oauthError(w, 503)
			return
		}
		s.oauthError(w, 401)
		return
	}
	if refreshGeneration != currentRefreshGeneration {
		s.oauthError(w, 401)
		return
	}
	if status != "Active" {
		if s.auditCredential(r.Context(), tx, "credential_blocked", account, client) != nil || tx.Commit(r.Context()) != nil {
			s.oauthError(w, 503)
			return
		}
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
	if e != nil || tx.Commit(r.Context()) != nil {
		s.oauthError(w, 503)
		return
	}
	writeTokens(w, access, next)
}

// AuthenticateAccess authorizes only local Discovery and Relay Authentication.
func (s *Server) AuthenticateAccess(ctx context.Context, raw string) (string, time.Time, error) {
	if len(raw) != 43 {
		return "", time.Time{}, errInvalidSession
	}
	for v := range s.Peppers {
		var account, status string
		var tokenGeneration, accountGeneration int64
		var until time.Time
		e := s.db.QueryRow(ctx, `SELECT g.account_id,a.status,g.credential_generation,a.credential_generation,t.expires_at FROM public.relay_access_tokens t JOIN public.login_grants g ON g.id=t.grant_id JOIN public.accounts a ON a.id=g.account_id WHERE t.credential_digest=$1 AND t.pepper_version=$2 AND t.expires_at>clock_timestamp() AND g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp()`, s.digest(v, "relay-access", raw), v).Scan(&account, &status, &tokenGeneration, &accountGeneration, &until)
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
