package persistence

import (
	"context"
)

type AdministratorSession struct {
	Email        string
	Verified     bool
	HostedDomain *string
	Recent       bool
}

type AccountAccessState struct {
	ID, Status, Subject  string
	Revision, Generation int64
	Deleting             bool
	WeeklyBytes          int64
	Sessions             int
	PlanID               *string
	WeeklyOverride       *int64
	SessionOverride      *int
}

type AdminAccountMutation struct {
	AccountID, PlanID, Action, NextStatus, Event, Reason, ActorEmail string
	ExpectedRevision                                                 int64
	Security                                                         bool
	WeeklyOverride                                                   *int64
	SessionOverride                                                  *int
	AuditID                                                          string
}

type AdminAccountMutationResult struct {
	Before                     AccountAccessState
	WeeklyBytes                int64
	Sessions                   int
	NewStatus                  string
	NewRevision, NewGeneration int64
	Security                   bool
}

type PlanCreate struct {
	ID, Name, ActorEmail, Reason string
	WeeklyBytes                  int64
	Sessions                     int
	Digest                       []byte
	AuditID                      string
}
type PlanCreateResult struct {
	Created  bool
	Matching bool
	Audited  bool
}
type PlanArchive struct {
	ID, ActorEmail, Reason, AuditID string
	ExpectedRevision                int64
}

type AccountAccessTransaction interface {
	AccountTransaction
	LockAdministratorAccounts(context.Context, string, string) error
	AdministratorSession(context.Context, string, []byte, uint64) (AdministratorSession, error)
	AccountAccessState(context.Context, string, bool) (AccountAccessState, error)
	ApplyAdminAccountMutation(context.Context, AdminAccountMutation) (AdminAccountMutationResult, error)
	CreateQuotaPlan(context.Context, PlanCreate) (PlanCreateResult, error)
	PlanCreateStatus(context.Context, string, []byte) (bool, error)
	ArchiveQuotaPlan(context.Context, PlanArchive) error
	RevokeAccountAccess(context.Context, string, []byte, uint64, string) (AccountAccessState, error)
}

type AccountAccessOperation interface {
	Context() context.Context
	BeginAccountAccess(context.Context) (AccountAccessTransaction, error)
	Close()
}

// AccountAccessWork admits protected administrator and owner account-access
// operations. The capability is explicit so these workflows do not discover
// transaction support after admission.
type AccountAccessWork interface {
	StartAccountAccess(context.Context) (AccountAccessOperation, error)
}
