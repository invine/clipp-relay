package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"clipp-relay/internal/auth/issuer"
	"clipp-relay/internal/persistence"
	"github.com/ory/fosite"
)

func beginRefreshTransaction(op persistence.RefreshOperation) (persistence.RefreshTransaction, error) {
	return op.BeginRefresh(op.Context())
}

func (s *Server) commitRefreshTx(ctx context.Context, tx persistence.RefreshTransaction) error {
	if s.commitRefresh != nil {
		return s.commitRefresh(ctx, tx)
	}
	return tx.Commit(ctx)
}

func (s *Server) nativeRefreshToken(w http.ResponseWriter, r *http.Request, client issuer.ClientType, raw string, digest [32]byte) {
	op, err := s.refresh.StartRefresh(r.Context())
	if err != nil {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	opOpen := true
	defer func() {
		if opOpen {
			op.Close()
		}
	}()
	lookupTx, err := beginRefreshTransaction(op)
	if err != nil {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	owner, err := lookupTx.RefreshOwner(op.Context(), digest)
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
	guardHeld := true
	defer func() {
		if guardHeld {
			guard.Unlock()
		}
	}()
	if !s.allowAccount(owner.Subject) {
		s.oauthError(w, http.StatusTooManyRequests)
		return
	}
	tx, err := beginRefreshTransaction(op)
	if err != nil {
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	txOpen := true
	defer func() {
		if txOpen {
			_ = tx.Rollback(op.Context())
		}
	}()
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

	ctx := issuer.InRefreshTransaction(op.Context(), tx, client, raw)
	request, err := s.nativeIssuer.NewAccessRequest(ctx, r, &fosite.DefaultSession{})
	if err != nil {
		if !issuer.ReplayCanCommit(ctx) {
			if errors.Is(err, fosite.ErrServerError) {
				s.oauthError(w, http.StatusServiceUnavailable)
			} else {
				s.oauthError(w, http.StatusUnauthorized)
			}
			return
		}
		if err = s.commitRefreshTx(op.Context(), tx); err != nil {
			// Commit retired the credential transaction. Confirm through a fresh,
			// independently admitted operation; cancellation cannot imply rollback.
			_ = tx.Rollback(context.Background())
			txOpen = false
			confirmed, confirmErr := s.confirmRefreshRevocation(owner.GrantID)
			if confirmErr != nil {
				markDiagnostic(r, "uncertain", "commit_unconfirmed")
				s.oauthError(w, http.StatusServiceUnavailable)
				return
			}
			if !confirmed {
				markDiagnostic(r, "failed", "database_unavailable")
				s.oauthError(w, http.StatusServiceUnavailable)
				return
			}
		}
		if s.deliverGrantRevocation(owner.GrantID) != nil {
			markDiagnostic(r, "failed", "invalidation_incomplete")
			s.oauthError(w, http.StatusServiceUnavailable)
			return
		}
		if txOpen {
			_ = tx.Rollback(context.Background())
			txOpen = false
		}
		op.Close()
		opOpen = false
		guard.Unlock()
		guardHeld = false
		if s.completeGrantRevocation(owner.GrantID) != nil {
			markDiagnostic(r, "failed", "invalidation_incomplete")
			s.oauthError(w, http.StatusServiceUnavailable)
			return
		}
		markDiagnostic(r, "refused", "replay_detected")
		s.oauthError(w, http.StatusUnauthorized)
		return
	}
	response, err := s.nativeIssuer.NewAccessResponse(ctx, request)
	if err != nil {
		if errors.Is(err, fosite.ErrServerError) {
			s.oauthError(w, http.StatusServiceUnavailable)
		} else {
			s.oauthError(w, http.StatusUnauthorized)
		}
		return
	}
	if err = s.commitRefreshTx(op.Context(), tx); err != nil {
		markDiagnostic(r, "uncertain", "commit_unconfirmed")
		s.oauthError(w, http.StatusServiceUnavailable)
		return
	}
	response.SetTokenType("Bearer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	s.nativeIssuer.WriteAccessResponse(op.Context(), w, request, response)
}

func (s *Server) confirmRefreshRevocation(grantID string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	op, err := s.refresh.StartRefresh(ctx)
	if err != nil {
		return false, err
	}
	defer op.Close()
	tx, err := beginRefreshTransaction(op)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	revoked, err := tx.ConfirmRefreshGrantRevoked(op.Context(), grantID)
	if err != nil {
		return false, err
	}
	return revoked, nil
}
