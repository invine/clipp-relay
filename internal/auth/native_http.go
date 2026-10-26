package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"clipp-relay/internal/auth/issuer"
)

func (s *Server) authenticateNativeAccess(ctx context.Context, raw string) (string, time.Time, error) {
	if s.nativeIssuer == nil {
		return "", time.Time{}, errors.New("native issuer unavailable")
	}
	op, err := s.persistence.Start(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	defer op.Close()
	tx, err := op.Begin(op.Context())
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback(op.Context())
	txCtx := issuer.InPersistenceTransaction(op.Context(), tx, "", raw)
	principal, expires, err := s.nativeIssuer.AuthenticateAccess(txCtx, raw)
	if err != nil {
		return "", time.Time{}, err
	}
	if err = tx.Commit(op.Context()); err != nil {
		return "", time.Time{}, err
	}
	return principal.AccountID, expires, nil
}

func (s *Server) deliverGrantRevocation(grantID string) error {
	if grantID == "" || s.nativeIssuer == nil || s.grantRevoked == nil || s.grantRevocationFallback == nil {
		return errors.New("grant invalidation is not configured")
	}
	s.nativeIssuer.CancelPendingValidations()
	if err := s.grantRevoked(grantID); err == nil {
		return nil
	}
	// A notification is an in-process operation. Retry it synchronously through
	// the composed manager recipient so an injected wrapper failure cannot leave
	// matching established sessions authorized after the committed mutation.
	return s.grantRevocationFallback(grantID)
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
	markClient(r, string(client))
	grant := r.PostForm.Get("grant_type")
	if grant == "refresh_token" {
		if trace := diagnosticFor(r); trace != nil {
			trace.event = "oauth.refresh"
		}
	}
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
	if purpose == issuer.AuthorizationCode {
		s.nativeAuthorizationCodeToken(w, r, client, rawToken, digest)
		return
	}
	s.nativeRefreshToken(w, r, client, rawToken, digest)
}
