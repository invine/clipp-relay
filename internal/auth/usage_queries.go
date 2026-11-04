package auth

import (
	"context"

	"clipp-relay/internal/portal"
)

// accountUsageQuery contains confirmed durable totals only. Consumers cannot use
// it for admission: no pending state, receipt, or protected eligibility exists.
type accountUsageQuery struct {
	Recorded, Remaining int64
	History             []portal.QuotaHistoryEntry
}

func (s *Server) adminAccountUsage(ctx context.Context, id string) (adminAccountRow, error) {
	read, err := portal.ReadAdminQuotaView(ctx, s.readViews, id)
	a := read.Account
	row := adminAccountRow{ID: a.ID, Email: a.Email, Status: a.Status, PlanID: a.PlanID, PlanName: a.PlanName, WeeklyOverride: a.WeeklyOverride, SessionOverride: a.SessionOverride, Revision: a.Revision, RecoveryHeld: a.RecoveryHeld, PlanArchived: a.PlanArchived, EffectiveBytes: a.EffectiveBytes, EffectiveSessions: a.EffectiveSessions}
	if err != nil {
		return row, err
	}
	row.Usage = &accountUsageQuery{Recorded: read.Recorded, Remaining: read.Remaining, History: read.History}
	return row, nil
}
