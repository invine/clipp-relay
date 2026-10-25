package auth

import (
	"context"
	"errors"
	"time"

	"clipp-relay/internal/auth/issuer"
	"github.com/jackc/pgx/v5"
	"github.com/ory/fosite"
)

var ErrInvalidAccess = errors.New("invalid relay access")

// RelayCredential contains account policy required for one live connection.
// The Peer ID is intentionally absent: accounts do not own device identities.
type RelayCredential struct {
	AccountID    string
	Generation   int64
	SessionLimit int
	ExpiresAt    time.Time
}

// AuthenticateRelay checks the token, grant, account status and current plan
// together. Database failure is distinct from a known invalid credential.
func (s *Server) AuthenticateRelay(ctx context.Context, raw string) (RelayCredential, error) {
	if s.nativeIssuer != nil {
		return s.authenticateNativeRelay(ctx, raw)
	}
	if len(raw) != 43 {
		return RelayCredential{}, ErrInvalidAccess
	}
	for v := range s.Peppers {
		var result RelayCredential
		var status string
		var grantGeneration int64
		var dbNow time.Time
		before := time.Now()
		err := s.db.QueryRow(ctx, `SELECT g.account_id,a.status,g.credential_generation,a.credential_generation,COALESCE(a.sessions_override,p.sessions),t.expires_at,clock_timestamp()
			FROM public.relay_access_tokens t JOIN public.login_grants g ON g.id=t.grant_id
			JOIN public.accounts a ON a.id=g.account_id JOIN public.quota_plans p ON p.id=a.plan_id
			WHERE NOT a.recovery_review_hold AND t.credential_digest=$1 AND t.pepper_version=$2 AND t.expires_at>clock_timestamp()
			AND g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp()`, s.digest(v, "relay-access", raw), v).Scan(&result.AccountID, &status, &grantGeneration, &result.Generation, &result.SessionLimit, &result.ExpiresAt, &dbNow)
		after := time.Now()
		if err == nil {
			if s.relayCredentialHook != nil {
				s.relayCredentialHook()
			}
			if _, uncertain := s.uncertainAccounts.Load(result.AccountID); uncertain {
				// Wait for a still-in-flight Commit to release the account row, then
				// re-read authority. A failed reconciliation keeps admission closed.
				var resolved string
				if err := s.db.QueryRow(ctx, `SELECT id FROM public.accounts WHERE id=$1 FOR SHARE`, result.AccountID).Scan(&resolved); err != nil {
					return RelayCredential{}, err
				}
				s.uncertainAccounts.Delete(result.AccountID)
				return s.AuthenticateRelay(ctx, raw)
			}
			if status != "Active" || grantGeneration != result.Generation {
				return RelayCredential{}, ErrInvalidAccess
			}
			remaining := result.ExpiresAt.Sub(dbNow) - after.Sub(before) - time.Second
			if remaining <= 0 {
				return RelayCredential{}, ErrInvalidAccess
			}
			result.ExpiresAt = after.Add(remaining)
			return result, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return RelayCredential{}, err
		}
	}
	return RelayCredential{}, ErrInvalidAccess
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
	unitCtx, release, err := s.db.StartUnit(ctx)
	if err != nil {
		return RelayCredential{}, err
	}
	defer release()
	tx, err := s.db.Begin(unitCtx)
	if err != nil {
		return RelayCredential{}, err
	}
	defer tx.Rollback(unitCtx)
	txCtx := issuer.InTransaction(unitCtx, tx, "", raw)
	principal, _, err := s.nativeIssuer.AuthenticateAccess(txCtx, raw)
	if err != nil {
		var protocolErr *fosite.RFC6749Error
		if errors.As(err, &protocolErr) {
			return RelayCredential{}, ErrInvalidAccess
		}
		return RelayCredential{}, err
	}
	digest, err := s.nativeIssuer.TokenDigest(issuer.AccessToken, raw)
	if err != nil {
		return RelayCredential{}, ErrInvalidAccess
	}
	var result RelayCredential
	var status string
	var accountGeneration, grantGeneration int64
	var dbNow time.Time
	before := time.Now()
	err = tx.QueryRow(unitCtx, `SELECT g.account_id::text,g.credential_generation,a.credential_generation,a.status,
		COALESCE(a.sessions_override,p.sessions),t.expires_at,clock_timestamp()
		FROM public.native_access_tokens t
		JOIN public.native_login_grants g ON g.id=t.grant_id
		JOIN public.accounts a ON a.id=g.account_id
		JOIN public.quota_plans p ON p.id=a.plan_id
		WHERE t.credential_digest=$1 AND g.id=$2 AND g.account_id=$3
		AND NOT a.recovery_review_hold AND a.status='Active'
		AND g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp()
		AND g.absolute_expires_at>clock_timestamp() AND t.expires_at>clock_timestamp()
		AND g.credential_generation=a.credential_generation`, digest[:], principal.GrantID, principal.AccountID).
		Scan(&result.AccountID, &grantGeneration, &accountGeneration, &status, &result.SessionLimit, &result.ExpiresAt, &dbNow)
	if errors.Is(err, pgx.ErrNoRows) {
		return RelayCredential{}, ErrInvalidAccess
	}
	if err != nil {
		return RelayCredential{}, err
	}
	result.Generation = accountGeneration
	if status != "Active" || grantGeneration != accountGeneration {
		return RelayCredential{}, ErrInvalidAccess
	}
	after := time.Now()
	if err = tx.Commit(unitCtx); err != nil {
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
		var resolved string
		if err := s.db.QueryRow(ctx, `SELECT id FROM public.accounts WHERE id=$1 FOR SHARE`, result.AccountID).Scan(&resolved); err != nil {
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
	remaining := result.ExpiresAt.Sub(dbNow) - after.Sub(before) - time.Second
	if remaining <= 0 {
		return RelayCredential{}, ErrInvalidAccess
	}
	result.ExpiresAt = after.Add(remaining)
	return result, nil
}
