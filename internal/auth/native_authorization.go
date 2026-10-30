package auth

import (
	"context"
	"errors"
	"net/http"

	"clipp-relay/internal/auth/issuer"
	"clipp-relay/internal/persistence"
	"github.com/ory/fosite"
)

// nativeAuthorizationCodeToken performs preliminary identity discovery under
// operation admission, releases the read transaction before waiting for the
// account identity guard, then rechecks authority in one credential transaction.
func (s *Server) commitAuthorizationTx(ctx context.Context, tx persistence.AuthorizationTransaction) error {
	if s.commitAuthorization != nil {
		return s.commitAuthorization(ctx, tx)
	}
	return tx.Commit(ctx)
}

func (s *Server) nativeAuthorizationCodeToken(w http.ResponseWriter, r *http.Request, client issuer.ClientType, raw string, digest [32]byte) {
	op, err := s.authorization.StartAuthorization(r.Context())
	if err != nil {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	defer op.Close()

	lookupTx, err := beginAuthorizationTransaction(op)
	if err != nil {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	owner, err := lookupTx.AuthorizationCodeOwner(op.Context(), digest)
	if errors.Is(err, persistence.ErrInvalidCredential) {
		_ = lookupTx.Rollback(op.Context())
		s.oauthError(w, http.StatusUnauthorized)
		return
	}
	if err != nil {
		_ = lookupTx.Rollback(op.Context())
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	if err = lookupTx.Commit(op.Context()); err != nil {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}

	guard := s.identityGuard(s.identityKey(owner.Subject))
	if !lockIdentity(op.Context(), guard) {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	defer guard.Unlock()
	if !s.allowAccount(owner.Subject) {
		s.oauthError(w, http.StatusTooManyRequests)
		return
	}

	tx, err := beginAuthorizationTransaction(op)
	if err != nil {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	defer tx.Rollback(op.Context())
	account, err := tx.LockAccountAuthorization(op.Context(), owner.AccountID)
	if errors.Is(err, persistence.ErrInvalidCredential) {
		s.oauthError(w, http.StatusUnauthorized)
		return
	}
	if err != nil {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	if account.Status != "Active" || account.Generation != owner.Generation {
		s.oauthError(w, http.StatusUnauthorized)
		return
	}

	activeGrants, err := tx.CountActiveLoginGrants(op.Context(), owner.AccountID, account.Generation)
	if err != nil {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	if activeGrants >= 20 {
		if err := tx.RecordCredentialAudit(op.Context(), "grant_cap", owner.AccountID, string(client)); err != nil {
			s.oauthError(w, http.StatusServiceUnavailable)
			return
		}
		if err := s.commitAuthorizationTx(op.Context(), tx); err != nil {
			markDiagnostic(r, "uncertain", "commit_unconfirmed")
			s.oauthError(w, http.StatusServiceUnavailable)
			return
		}
		markDiagnostic(r, "refused", "grant_limit")
		s.oauthError(w, http.StatusTooManyRequests)
		return
	}

	requestContext := issuer.InAuthorizationTransaction(op.Context(), tx, client, raw)
	accessRequest, err := s.nativeIssuer.NewAccessRequest(requestContext, r, &fosite.DefaultSession{})
	if err != nil {
		if errors.Is(err, fosite.ErrServerError) {
			s.oauthError(w, http.StatusServiceUnavailable)
		} else {
			s.oauthError(w, http.StatusUnauthorized)
		}
		return
	}
	response, err := s.nativeIssuer.NewAccessResponse(requestContext, accessRequest)
	if err != nil {
		if errors.Is(err, fosite.ErrServerError) {
			s.oauthError(w, http.StatusServiceUnavailable)
		} else {
			s.oauthError(w, http.StatusUnauthorized)
		}
		return
	}
	if err = s.commitAuthorizationTx(op.Context(), tx); err != nil {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	response.SetTokenType("Bearer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	s.nativeIssuer.WriteAccessResponse(op.Context(), w, accessRequest, response)
}
