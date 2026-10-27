package persistence

import (
	"context"
	"time"
)

type DeletionOwner struct{ Subject string }
type DeletionAdmissionRequest struct {
	AccountID, OperationID, ExpectedSubject string
	SessionDigest                           []byte
	PepperVersion                           uint64
}
type DeletionAdmission struct {
	Subject     string
	WeeklyBytes int64
	Sessions    int
}
type DeletionOperation struct {
	ID, AccountID        string
	AdmittedAt           time.Time
	EventSequence        *int64
	EventHash, EventData []byte
	JournalGeneration    *int64
	Attempts             int
}
type DeletionEventPreparation struct {
	OperationID        string
	Sequence           int64
	Hash, Data         []byte
	Generation         int64
	ExpectedSequence   *int64
	ExpectedHash       []byte
	ExpectedGeneration *int64
}

// DeletionTransaction owns atomic admission and finalization, including all
// account credentials, capacity, retention, identity erasure and audit writes.
type DeletionTransaction interface {
	Transaction
	DeletionOwner(context.Context, string) (DeletionOwner, error)
	AdmitDeletion(context.Context, DeletionAdmissionRequest) (DeletionAdmission, error)
	FinalizeDeletion(context.Context, string, string, string, uint64, []byte, string) error
}

type DeletionRepository interface {
	NextDeletion(context.Context) (DeletionOperation, error)
	DeletionStatus(context.Context, string) (bool, error)
	DeletionIdentity(context.Context, string) (string, error)
	RecordDeletionRetry(context.Context, string, int, float64) error
	DeletionSignals(context.Context) (DeletionReadSignals, error)
	BeginDeletion(context.Context) (DeletionTransaction, error)
	PrepareDeletionEvent(context.Context, DeletionEventPreparation) (bool, error)
	DeletionCommitted(context.Context, string) (bool, error)
}
