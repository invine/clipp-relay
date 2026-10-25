package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"clipp-relay/internal/auth/issuer"
	"github.com/jackc/pgx/v5"
	"github.com/ory/fosite"
)

func (s *Server) authenticateNativeAccess(ctx context.Context, raw string) (string, time.Time, error) {
	if s.nativeIssuer == nil {
		return "", time.Time{}, errors.New("native issuer unavailable")
	}
	unitCtx, release, err := s.db.StartUnit(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer release()
	tx, err := s.db.Begin(unitCtx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback(unitCtx)
	txCtx := issuer.InTransaction(unitCtx, tx, "", raw)
	principal, expires, err := s.nativeIssuer.AuthenticateAccess(txCtx, raw)
	if err != nil {
		return "", time.Time{}, err
	}
	if err = tx.Commit(unitCtx); err != nil {
		return "", time.Time{}, err
	}
	return principal.AccountID, expires, nil
}

func (s *Server) nativeToken(w http.ResponseWriter, r *http.Request) {
	if s.nativeIssuer == nil || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("Content-Encoding") != "" || r.URL.RawQuery != "" {
		s.oauthError(w, 400)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil || len(r.PostForm) < 2 {
		s.oauthError(w, 400)
		return
	}
	client := issuer.ClientType(r.PostForm.Get("client_id"))
	if client != issuer.Electron && client != issuer.Android && client != issuer.Extension {
		s.oauthError(w, 400)
		return
	}
	grant := r.PostForm.Get("grant_type")
	var purpose issuer.Purpose
	allowed := map[string]bool{"grant_type": true, "client_id": true}
	switch grant {
	case "authorization_code":
		purpose = issuer.AuthorizationCode
		allowed["code"], allowed["redirect_uri"], allowed["code_verifier"] = true, true, true
	case "refresh_token":
		purpose = issuer.RefreshToken
		allowed["refresh_token"] = true
	default:
		s.oauthError(w, 400)
		return
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
	tokenField := "code"
	if purpose == issuer.RefreshToken {
		tokenField = "refresh_token"
	}
	rawToken := r.PostForm.Get(tokenField)
	digest, err := s.nativeIssuer.TokenDigest(purpose, rawToken)
	if err != nil {
		s.oauthError(w, 401)
		return
	}
	var account string
	var tokenGeneration int64
	if purpose == issuer.AuthorizationCode {
		err = s.Pool.QueryRow(r.Context(), `SELECT account_id::text,credential_generation FROM public.native_authorization_codes WHERE credential_digest=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp()`, digest[:]).Scan(&account, &tokenGeneration)
	} else {
		err = s.Pool.QueryRow(r.Context(), `SELECT g.account_id::text,g.credential_generation FROM public.native_refresh_generations rg JOIN public.native_login_grants g ON g.id=rg.grant_id WHERE rg.credential_digest=$1`, digest[:]).Scan(&account, &tokenGeneration)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		s.oauthError(w, 401)
		return
	}
	if err != nil {
		s.oauthError(w, 503)
		return
	}
	unitCtx, releaseUnit, err := s.db.StartUnit(r.Context())
	if err != nil {
		s.oauthError(w, 503)
		return
	}
	defer releaseUnit()
	var subject string
	if err = s.Pool.QueryRow(unitCtx, `SELECT subject FROM public.accounts WHERE id=$1`, account).Scan(&subject); err != nil {
		s.oauthError(w, 401)
		return
	}
	guard := s.identityGuard(s.identityKey(subject))
	if !lockIdentity(unitCtx, guard) {
		s.oauthError(w, 503)
		return
	}
	defer guard.Unlock()
	if !s.allowAccount(subject) {
		s.oauthError(w, 429)
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
	if status != "Active" || generation != tokenGeneration {
		s.oauthError(w, 401)
		return
	}
	ctx := issuer.InTransaction(unitCtx, tx, client, rawToken)
	fositeRequest, err := s.nativeIssuer.NewAccessRequest(ctx, r, &fosite.DefaultSession{})
	if err != nil {
		if issuer.ReplayCanCommit(ctx) {
			if err = s.commitCredentialTx(unitCtx, tx); err != nil {
				markDiagnostic(r, "uncertain", "commit_unconfirmed")
				s.oauthError(w, 503)
				return
			}
			s.oauthError(w, 401)
			return
		}
		if errors.Is(err, fosite.ErrServerError) {
			s.oauthError(w, 503)
		} else {
			s.oauthError(w, 401)
		}
		return
	}
	response, err := s.nativeIssuer.NewAccessResponse(ctx, fositeRequest)
	if err != nil {
		if errors.Is(err, fosite.ErrServerError) {
			s.oauthError(w, 503)
		} else {
			s.oauthError(w, 401)
		}
		return
	}
	if err = s.commitCredentialTx(unitCtx, tx); err != nil {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		s.oauthError(w, 503)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	s.nativeIssuer.WriteAccessResponse(unitCtx, w, fositeRequest, response)
}
