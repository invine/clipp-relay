package auth

import (
	"context"
	"errors"
	"time"

	"clipp-relay/internal/auth/issuer"
	"clipp-relay/internal/persistence"
	"github.com/ory/fosite"
)

var ErrInvalidAccess = errors.New("invalid relay access")

// RelayCredential contains account policy required for one live connection.
// The Peer ID is intentionally absent: accounts do not own device identities.
type RelayCredential struct {
	AccountID    string
	GrantID      string
	Generation   int64
	SessionLimit int
	ExpiresAt    time.Time
}

// AuthenticateRelay checks the token, grant, account status and current plan
// together. Database failure is distinct from a known invalid credential.
func (s *Server) AuthenticateRelay(ctx context.Context, raw string) (RelayCredential, error) {
	if s.nativeIssuer == nil {
		return RelayCredential{}, ErrInvalidAccess
	}
	return s.authenticateNativeRelay(ctx, raw)
}

// authenticateNativeRelay validates the library-managed Fosite access-token
// signature and the typed native record in one transaction, then reads current
// account and plan authority from that same database snapshot. It intentionally
// never asks Fosite to authenticate a refresh token or authorization code.
func (s *Server) authenticateNativeRelay(ctx context.Context, raw string) (RelayCredential, error) {
	return s.authenticateNativeRelayPass(ctx, raw, true)
}

func (s *Server) authenticateNativeRelayPass(ctx context.Context, raw string, runObservationHook bool) (RelayCredential, error) {
	if len(raw) != 94 {
		return RelayCredential{}, ErrInvalidAccess
	}
	op, err := s.persistence.Start(ctx)
	if err != nil {
		return RelayCredential{}, err
	}
	defer op.Close()
	tx, err := op.Begin(op.Context())
	if err != nil {
		return RelayCredential{}, err
	}
	defer tx.Rollback(op.Context())
	txCtx := issuer.InPersistenceTransaction(op.Context(), tx, "", raw)
	principal, _, err := s.nativeIssuer.AuthenticateAccess(txCtx, raw)
	if err != nil {
		var protocolErr *fosite.RFC6749Error
		if errors.As(err, &protocolErr) || errors.Is(err, persistence.ErrInvalidCredential) || errors.Is(err, fosite.ErrNotFound) {
			return RelayCredential{}, ErrInvalidAccess
		}
		return RelayCredential{}, err
	}
	digest, err := s.nativeIssuer.TokenDigest(issuer.AccessToken, raw)
	if err != nil {
		return RelayCredential{}, ErrInvalidAccess
	}
	before := time.Now()
	stored, err := tx.RelayCredential(op.Context(), digest, principal.GrantID, principal.AccountID)
	if errors.Is(err, persistence.ErrInvalidCredential) {
		return RelayCredential{}, ErrInvalidAccess
	}
	if err != nil {
		return RelayCredential{}, err
	}
	var result RelayCredential
	result = RelayCredential{AccountID: stored.AccountID, GrantID: stored.GrantID, Generation: stored.Generation, SessionLimit: stored.SessionLimit, ExpiresAt: stored.ExpiresAt}
	if stored.AccountStatus != "Active" || stored.ReviewHold || stored.Generation != principal.Generation {
		return RelayCredential{}, ErrInvalidAccess
	}
	after := time.Now()
	if err = tx.Commit(op.Context()); err != nil {
		return RelayCredential{}, err
	}
	observationWaited := false
	if runObservationHook && s.relayCredentialHook != nil {
		hook := s.relayCredentialHook
		hook()
		observationWaited = true
	}
	if _, uncertain := s.uncertainAccounts.Load(result.AccountID); uncertain {
		// Wait for any ambiguous account mutation to finish before accepting its
		// credential snapshot, then perform a fresh token and authority lookup.
		if err := s.confirmUncertainAccount(ctx, result.AccountID); err != nil {
			return RelayCredential{}, err
		}
		s.uncertainAccounts.Delete(result.AccountID)
		return s.authenticateNativeRelayPass(ctx, raw, false)
	}
	if observationWaited {
		// Test barriers model a required wait between the initial observation
		// and admission. Re-read after release so expiry and changed policy are
		// decided from the authority that exists when the wait is over.
		return s.authenticateNativeRelayPass(ctx, raw, false)
	}
	remaining := result.ExpiresAt.Sub(stored.DatabaseNow) - after.Sub(before) - time.Second
	if remaining <= 0 {
		return RelayCredential{}, ErrInvalidAccess
	}
	result.ExpiresAt = after.Add(remaining)
	return result, nil
}

func (s *Server) confirmUncertainAccount(ctx context.Context, accountID string) error {
	op, err := s.persistence.Start(ctx)
	if err != nil {
		return err
	}
	defer op.Close()
	tx, err := op.Begin(op.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(op.Context())
	account, ok := tx.(persistence.AccountTransaction)
	if !ok {
		return persistence.ErrUnavailable
	}
	if _, err = account.AccountStateAfterUncertain(op.Context(), accountID, ""); err != nil {
		return err
	}
	return tx.Commit(op.Context())
}
