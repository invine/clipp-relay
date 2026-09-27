package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
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
	if len(raw) != 43 {
		return RelayCredential{}, ErrInvalidAccess
	}
	for v := range s.Peppers {
		var result RelayCredential
		var status string
		var grantGeneration int64
		var dbNow time.Time
		before := time.Now()
		err := s.Pool.QueryRow(ctx, `SELECT g.account_id,a.status,g.credential_generation,a.credential_generation,p.sessions,t.expires_at,clock_timestamp()
			FROM public.relay_access_tokens t JOIN public.login_grants g ON g.id=t.grant_id
			JOIN public.accounts a ON a.id=g.account_id JOIN public.quota_plans p ON p.id=a.plan_id
			WHERE t.credential_digest=$1 AND t.pepper_version=$2 AND t.expires_at>clock_timestamp()
			AND g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp()`, s.digest(v, "relay-access", raw), v).Scan(&result.AccountID, &status, &grantGeneration, &result.Generation, &result.SessionLimit, &result.ExpiresAt, &dbNow)
		after := time.Now()
		if err == nil {
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
