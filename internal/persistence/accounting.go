package persistence

import (
	"context"
	"errors"
	"time"
)

var (
	ErrUsageRetired  = errors.New("account usage is retired")
	ErrConflict      = errors.New("usage settlement conflicts with durable state")
	ErrKnownRollback = errors.New("persistence transaction rolled back")
)

// AccountingEligibility is the authoritative policy and usage snapshot for
// one Relay Account at database time.
type AccountingEligibility struct {
	DatabaseNow   time.Time
	CapBytes      int64
	SessionLimit  int
	RecordedBytes int64
	Sequence      int64
	OperationID   string
	DeltaBytes    int64
}

// RecordedUsage is the total and compact latest settlement receipt for one
// Weekly Quota Window.
type RecordedUsage struct {
	RecordedBytes int64
	Sequence      int64
	OperationID   string
	DeltaBytes    int64
	WeekStart     time.Time
	DatabaseNow   time.Time
}

// UsageSettlement is one immutable batch attributed to its original week.
type UsageSettlement struct {
	AccountID   string
	WeekStart   time.Time
	OperationID string
	DeltaBytes  int64
	Sequence    int64
}

type SettlementAccount struct {
	Deleted bool
}

// AccountingTransaction exposes domain operations for settlement. The
// PostgreSQL adapter owns account locking, row mapping and atomic receipt
// updates; Quota retains week eligibility and retry policy.
type AccountingTransaction interface {
	LockSettlementAccount(context.Context, string) (SettlementAccount, error)
	DatabaseTime(context.Context) (time.Time, error)
	SettleUsage(context.Context, UsageSettlement) (RecordedUsage, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}

type AccountingOperation interface {
	Context() context.Context
	BeginAccounting(context.Context) (AccountingTransaction, error)
	Close()
}

// Accounting is the narrow persistence surface used by Quota. Every read is
// admitted by the shared work budget; settlement holds one admitted operation
// while it waits for Quota's account/week serialization guard.
type Accounting interface {
	ProbeDatabaseTime(context.Context) (time.Time, error)
	ReadAccountingEligibility(context.Context, string) (AccountingEligibility, error)
	ReadRecordedUsage(context.Context, string, time.Time) (RecordedUsage, error)
	StartSettlement(context.Context) (AccountingOperation, error)
}
