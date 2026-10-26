package persistence

import (
	"context"
	"errors"
	"time"
)

var ErrInvalidCleanupOperation = errors.New("invalid cleanup operation")

// CleanupOperation identifies one bounded retention operation. The PostgreSQL
// adapter owns the statement and its row mapping for each operation.
type CleanupOperation uint8

const (
	CleanupRetainedUsage CleanupOperation = iota + 1
	CleanupWeeklyUsage
	CleanupAuthorizationCodes
	CleanupAccessTokens
	CleanupTerminatedAccessTokens
	CleanupRefreshGenerations
	CleanupLoginGrants
	CleanupIdlePortalSessions
	CleanupAbsolutePortalSessions
	CleanupHiddenAuthorizationCodes
	CleanupHiddenAccessTokens
	CleanupHiddenRefreshGenerations
	CleanupHiddenLoginGrants
	CleanupHiddenPortalSessions
	CleanupHiddenWeeklyUsage
	CleanupDeletionOperations
	CleanupAuditEvents
	CleanupDeletedAccounts
	CleanupExpiredPendingAccounts
)

type CleanupBatch struct {
	Operation CleanupOperation
	Limit     int64
	Cursor    string
}

type CleanupBatchResult struct {
	Removed    int64
	Candidates int64
	Take       int64
	NextCursor string
}

type CleanupHealth struct {
	OldestAge      time.Duration
	BreachEpisodes uint64
}

type PepperCoverage struct {
	PortalSessions []uint64
	RetainedUsage  []uint64
}

// Maintenance owns bounded retention statements, cleanup health persistence,
// and credential-key coverage reads. Functional modules retain policy.
type Maintenance interface {
	CleanupHealth(context.Context) (CleanupHealth, error)
	RecordCleanupBreach(context.Context, bool, int64) error
	CleanupBatch(context.Context, CleanupBatch) (CleanupBatchResult, error)
	PepperCoverage(context.Context) (PepperCoverage, error)
}
