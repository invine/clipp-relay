package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"clipp-relay/internal/portal"
)

type ownerUsageProfile struct {
	Status, Email string
	WeekStart     time.Time
	Verified      bool
	RecoveryHeld  bool
	HostedDomain  *string
	WeeklyBytes   *int64
	Sessions      *int
	Usage         int64
	HistoryJSON   []byte
}

// One statement rechecks portal credentials and snapshots policy and usage.
func (s *Server) ownerUsageProfile(r *http.Request, id string, version uint64) (ownerUsageProfile, error) {
	read, err := s.readViews.OwnerProfile(r.Context(), id, s.sessionDigest(r, version), version)
	return ownerUsageProfile{Status: read.Status, Email: read.Email, WeekStart: read.WeekStart, Verified: read.Verified, RecoveryHeld: read.RecoveryHeld, HostedDomain: read.HostedDomain, WeeklyBytes: read.WeeklyBytes, Sessions: read.Sessions, Usage: read.Usage, HistoryJSON: read.HistoryJSON}, err
}

// accountUsageQuery contains confirmed durable totals only. Consumers cannot use
// it for admission: no pending state, receipt, or protected eligibility exists.
type accountUsageQuery struct {
	Recorded, Remaining int64
	History             []portal.QuotaHistoryEntry
}

func (s *Server) adminAccountUsage(ctx context.Context, id string) (adminAccountRow, error) {
	read, err := s.readViews.AdminAccount(ctx, id)
	a := adminAccountRow{ID: read.ID, Email: read.Email, Status: read.Status, PlanID: read.PlanID, PlanName: read.PlanName, WeeklyOverride: read.WeeklyOverride, SessionOverride: read.SessionOverride, Revision: read.Revision, RecoveryHeld: read.RecoveryHeld, PlanArchived: read.PlanArchived, EffectiveBytes: read.EffectiveBytes, EffectiveSessions: read.EffectiveSessions}
	var usage accountUsageQuery
	if err != nil {
		return a, err
	}
	usage.Recorded, usage.Remaining = read.Recorded, read.Remaining
	if err = json.Unmarshal(read.HistoryJSON, &usage.History); err != nil {
		return a, err
	}
	a.Usage = &usage
	return a, nil
}
