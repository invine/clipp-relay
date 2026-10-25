package auth

import (
	"context"
	"errors"
)

// ErrRetainedPepperUnavailable allows startup to keep existing accounts online
// while registration remains closed until the current-week key is restored.
var ErrRetainedPepperUnavailable = errors.New("retained usage key unavailable")

// ValidatePepperCoverage protects Portal Sessions and current-week retained
// usage. OAuth credentials use the independent native signing key.
func (s *Server) ValidatePepperCoverage(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `
SELECT DISTINCT pepper_version FROM public.portal_sessions
WHERE LEAST(idle_expires_at,absolute_expires_at)>=clock_timestamp()-interval '24 hours'`)
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
