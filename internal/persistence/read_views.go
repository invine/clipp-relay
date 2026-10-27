package persistence

import (
	"context"
	"time"
)

// OwnerProfile is the privacy-scoped portal snapshot for one authenticated owner.
type OwnerProfile struct {
	Status, Email          string
	WeekStart              time.Time
	Verified, RecoveryHeld bool
	HostedDomain           *string
	WeeklyBytes            *int64
	Sessions               *int
	Usage                  int64
	HistoryJSON            []byte
}

// AdminAccount is a bounded administrative account projection.
type AdminAccount struct {
	ID, Email, Status, PlanID, PlanName, WeeklyOverride, SessionOverride string
	Revision                                                             int64
	RecoveryHeld                                                         bool
	Recorded, Remaining                                                  int64
	EffectiveBytes                                                       int64
	EffectiveSessions                                                    int
	PlanArchived                                                         bool
	HistoryJSON                                                          []byte
}

type AdminPlan struct {
	ID, Name string
	Bytes    int64
	Sessions int
	Revision int64
	Archived bool
}

type AccountCounts struct{ Pending, Active, Suspended, Denied int64 }
type AdminHome struct {
	Counts        AccountCounts
	FilteredCount int64
	Accounts      []AdminAccount
	Plans         []AdminPlan
}

// ReadViews contains only consumer-facing snapshots. It cannot authorize or mutate.
type ReadViews interface {
	OwnerProfile(context.Context, string, []byte, uint64) (OwnerProfile, error)
	AdminHome(context.Context, string, string, string, string, int) (AdminHome, error)
	AdminPlans(context.Context, string, int) ([]AdminPlan, error)
	AdminAccount(context.Context, string) (AdminAccount, error)
	AdminPlan(context.Context, string) (AdminPlan, error)
	ActiveLoginGrantCount(context.Context, string) (int, error)
	DeletionSignals(context.Context) (DeletionReadSignals, error)
}

type DeletionReadSignals struct {
	Pending   int
	OldestAge time.Duration
}
