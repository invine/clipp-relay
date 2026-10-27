package persistence

import (
	"context"
	"time"
)

// AccountRegistration describes the provider-verified identity and latest
// presentation metadata to persist for one completed Google sign-in.
type AccountRegistration struct {
	ID, Issuer, Subject, Email string
	EmailVerified              bool
	HostedDomain               *string
}

// RegisteredAccount is the durable account identity observed by the callback.
type RegisteredAccount struct {
	ID         string
	Generation int64
	Created    bool
}

// AccountStateAfterUncertain is the authoritative state used to resolve a
// previous account mutation or deletion whose commit acknowledgement was lost.
type AccountStateAfterUncertain struct {
	Revision, Generation     int64
	Status                   string
	DeletionStarted          bool
	DeletionOperationPresent bool
}

// RetainedUsageRecord is the privacy-minimal current-week quota record that
// may be imported once when an External Identity registers a fresh account.
type RetainedUsageRecord struct {
	PepperVersion  uint64
	IdentityDigest [32]byte
	Week           time.Time
	RecordedBytes  int64
}

// PortalSessionOwner is the durable projection required to authenticate a
// Portal Session while keeping expiry and account eligibility authoritative in
// PostgreSQL.
type PortalSessionOwner struct {
	AccountID, Status, Email, Subject string
	Generation                        int64
	PepperVersion                     uint64
	CSRFHash                          []byte
}

// AccountTransaction expresses the complete repository operations required by
// Google registration and Portal Session workflows. All writes participate in
// the caller-owned transaction.
type AccountTransaction interface {
	Transaction
	UpsertRelayAccount(context.Context, AccountRegistration) (RegisteredAccount, error)
	AccountStateAfterUncertain(context.Context, string, string) (AccountStateAfterUncertain, error)
	RecordAccountCreated(context.Context, string, string) error
	CurrentRetainedUsageWeek(context.Context) (time.Time, error)
	RetainedUsageVersions(context.Context) ([]uint64, error)
	RetainedUsageForIdentity(context.Context, uint64, [32]byte, time.Time) (RetainedUsageRecord, bool, error)
	ImportRetainedUsage(context.Context, string, RetainedUsageRecord) error
	CreatePortalSession(context.Context, [32]byte, uint64, string, int64, [32]byte) error
	PortalSessionForOwner(context.Context, [32]byte, uint64) (PortalSessionOwner, error)
	TouchPortalSession(context.Context, [32]byte, uint64, string, int64) (bool, error)
	DeletePortalSession(context.Context, [32]byte, uint64) (bool, error)
}
