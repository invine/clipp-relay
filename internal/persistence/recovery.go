package persistence

import (
	"context"
	"time"
)

type RecoveryRun struct {
	RunID, Source, Target string
	SchemaRevision        int
	JournalHead           []byte
}
type RecoveredAccount struct {
	Subject string
	Deleted bool
}

type RecoveryTransaction interface {
	Transaction
	LockRecovery(context.Context) error
	FindRecoveryRun(context.Context, string) (RecoveryRun, bool, error)
	LockRecoveryAccounts(context.Context) error
	InvalidateRestoredCredentials(context.Context) error
	RetainedPepperVersions(context.Context) ([]uint64, error)
	RecoveredAccount(context.Context, string) (RecoveredAccount, bool, error)
	CurrentQuotaWeek(context.Context) (time.Time, error)
	RecordedUsage(context.Context, string, time.Time) (int64, bool, error)
	RecoveredRetainedUsage(context.Context, uint64, []byte, time.Time) (int64, bool, error)
	StoreRecoveredRetainedUsage(context.Context, uint64, []byte, time.Time, int64) error
	CompleteRecoveredAccount(context.Context, string, string) error
	RecordRecoveryRun(context.Context, RecoveryRun) error
}

type RecoveryRepository interface {
	SchemaRevision() int
	CurrentDatabase(context.Context) (string, error)
	FindRecoveryRun(context.Context, string) (RecoveryRun, bool, error)
	BeginRecovery(context.Context) (RecoveryTransaction, error)
}
