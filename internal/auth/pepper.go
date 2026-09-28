package auth

import (
	"context"
	"errors"
)

// ErrRetainedPepperUnavailable allows startup to keep existing accounts online
// while registration remains closed until the current-week key is restored.
var ErrRetainedPepperUnavailable = errors.New("retained usage key unavailable")

// ValidatePepperCoverage rejects removal of a version that still protects a
// credential inside its expiry and 24-hour cleanup window. Current-week
// retained usage is separately reported for registration-only fail closure.
func (s *Server) ValidatePepperCoverage(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `
SELECT DISTINCT pepper_version FROM (
 SELECT pepper_version FROM public.authorization_transactions WHERE expires_at>=clock_timestamp()-interval '24 hours'
 UNION ALL SELECT pepper_version FROM public.authorization_codes WHERE LEAST(COALESCE(consumed_at,expires_at),expires_at)>=clock_timestamp()-interval '24 hours'
 UNION ALL SELECT pepper_version FROM public.portal_sessions WHERE LEAST(idle_expires_at,absolute_expires_at)>=clock_timestamp()-interval '24 hours'
 UNION ALL SELECT t.pepper_version FROM public.relay_access_tokens t JOIN public.login_grants g ON g.id=t.grant_id
  WHERE LEAST(t.expires_at,COALESCE(g.terminated_at,t.expires_at),g.idle_expires_at,g.absolute_expires_at)>=clock_timestamp()-interval '24 hours'
 UNION ALL SELECT f.pepper_version FROM public.refresh_generations f JOIN public.login_grants g ON g.id=f.grant_id
  WHERE LEAST(COALESCE(g.terminated_at,g.idle_expires_at),g.idle_expires_at,g.absolute_expires_at)>=clock_timestamp()-interval '24 hours'
) protected`)
	if err != nil {
		return errors.New("pepper coverage unavailable")
	}
	defer rows.Close()
	for rows.Next() {
		var version uint64
		if rows.Scan(&version) != nil || len(s.Peppers[version]) == 0 {
			return errors.New("pepper version still protects credentials")
		}
	}
	if rows.Err() != nil {
		return errors.New("pepper coverage unavailable")
	}
	retained, err := s.db.Query(ctx, `SELECT DISTINCT pepper_version FROM public.retained_quota_usage WHERE week_start>=date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`)
	if err != nil {
		return errors.New("pepper coverage unavailable")
	}
	defer retained.Close()
	for retained.Next() {
		var version uint64
		if retained.Scan(&version) != nil || len(s.Peppers[version]) == 0 {
			return ErrRetainedPepperUnavailable
		}
	}
	if retained.Err() != nil {
		return errors.New("pepper coverage unavailable")
	}
	return nil
}
