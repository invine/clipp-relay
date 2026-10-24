package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"clipp-relay/internal/portal"
)

// EnableRecordedUsagePresentation stages query-only views with observed Relay
// composition. It must be set before serving and grants no command authority.
func (s *Server) EnableRecordedUsagePresentation() { s.recordedUsagePresentation = true }

type ownerUsageProfile struct {
	Status, Email string
	Verified      bool
	HostedDomain  *string
	WeeklyBytes   *int64
	Sessions      *int
	Usage         int64
	HistoryJSON   []byte
}

// One statement rechecks portal credentials and snapshots policy and usage.
func (s *Server) ownerUsageProfile(r *http.Request, id string, version uint64) (ownerUsageProfile, error) {
	var p ownerUsageProfile
	query := `WITH current_week AS MATERIALIZED (SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date AS monday)
SELECT a.status,a.email,a.email_verified,a.hosted_domain,COALESCE(a.weekly_bytes_override,p.weekly_bytes),COALESCE(a.sessions_override,p.sessions),
COALESCE((SELECT u.committed_bytes FROM public.weekly_quota_usage u WHERE u.account_id=a.id AND u.week_start=(SELECT monday FROM current_week)),0),
(SELECT jsonb_agg(jsonb_build_object('week',w.week_start,'committed',COALESCE(u.committed_bytes,0)) ORDER BY w.week_start DESC)
 FROM (SELECT ((SELECT monday FROM current_week)-7*n)::date AS week_start FROM generate_series(0,12) n) w
 LEFT JOIN public.weekly_quota_usage u ON u.account_id=a.id AND u.week_start=w.week_start)
FROM public.portal_sessions ps JOIN public.accounts a ON a.id=ps.account_id
LEFT JOIN public.quota_plans p ON p.id=a.plan_id
WHERE a.id=$1 AND ps.credential_digest=$2 AND ps.pepper_version=$3
AND ps.credential_generation=a.credential_generation
AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp()
AND (a.status<>'Pending' OR a.last_portal_login_at>=clock_timestamp()-interval '90 days')`
	if s.recordedUsagePresentation {
		query = strings.ReplaceAll(strings.ReplaceAll(query, "weekly_quota_usage", "weekly_relay_usage"), "committed_bytes", "recorded_bytes")
	}
	err := s.db.QueryRow(r.Context(), query, id, s.sessionDigest(r, version), version).Scan(&p.Status, &p.Email, &p.Verified, &p.HostedDomain, &p.WeeklyBytes, &p.Sessions, &p.Usage, &p.HistoryJSON)
	return p, err
}

// accountUsageQuery contains confirmed durable totals only. Consumers cannot use
// it for admission: no pending state, receipt, or protected eligibility exists.
type accountUsageQuery struct {
	Recorded, Remaining int64
	History             []portal.QuotaHistoryEntry
}

func (s *Server) adminAccountUsage(ctx context.Context, id string) (adminAccountRow, error) {
	var a adminAccountRow
	if !s.recordedUsagePresentation {
		err := s.db.QueryRow(ctx, `SELECT id,email,status,COALESCE(plan_id::text,''),COALESCE(weekly_bytes_override::text,''),COALESCE(sessions_override::text,''),revision FROM public.accounts WHERE id=$1 AND deletion_started_at IS NULL`, id).Scan(&a.ID, &a.Email, &a.Status, &a.PlanID, &a.WeeklyOverride, &a.SessionOverride, &a.Revision)
		return a, err
	}
	var usage accountUsageQuery
	var history []byte
	err := s.db.QueryRow(ctx, `WITH current_week AS MATERIALIZED (SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date AS monday)
 SELECT a.id,a.email,a.status,COALESCE(a.plan_id::text,''),COALESCE(a.weekly_bytes_override::text,''),COALESCE(a.sessions_override::text,''),a.revision,COALESCE(u.recorded_bytes,0),GREATEST(0,COALESCE(a.weekly_bytes_override,p.weekly_bytes,0)-COALESCE(u.recorded_bytes,0)),
 (SELECT jsonb_agg(jsonb_build_object('week',w.week_start,'committed',COALESCE(h.recorded_bytes,0)) ORDER BY w.week_start DESC) FROM (SELECT ((SELECT monday FROM current_week)-7*n)::date AS week_start FROM generate_series(0,12) n) w LEFT JOIN public.weekly_relay_usage h ON h.account_id=a.id AND h.week_start=w.week_start)
 FROM public.accounts a LEFT JOIN public.quota_plans p ON p.id=a.plan_id LEFT JOIN public.weekly_relay_usage u ON u.account_id=a.id AND u.week_start=(SELECT monday FROM current_week) WHERE a.id=$1 AND a.deletion_started_at IS NULL`, id).Scan(&a.ID, &a.Email, &a.Status, &a.PlanID, &a.WeeklyOverride, &a.SessionOverride, &a.Revision, &usage.Recorded, &usage.Remaining, &history)
	if err != nil {
		return a, err
	}
	if err = json.Unmarshal(history, &usage.History); err != nil {
		return a, err
	}
	a.Usage = &usage
	return a, nil
}
