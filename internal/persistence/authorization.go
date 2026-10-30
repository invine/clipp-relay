package persistence

import (
	"context"
	"time"
)

// AuthorizationCode binds one public client, redirect, PKCE challenge and
// Relay Account to a stable Login Grant identity.
type AuthorizationCode struct {
	AccountID, ClientType, RedirectURI, Challenge, GrantID string
	Generation                                             int64
	ExpiresAt                                              time.Time
	Consumed                                               bool
}

// AccountAuthorization is the database-authoritative account state observed
// while locking the account for consent or code issuance.
type AccountAuthorization struct {
	Status     string
	Generation int64
}

// PortalSessionForConsent is the minimal browser-session projection required
// to approve native-client consent.
type PortalSessionForConsent struct {
	AccountID, Subject string
	Generation         int64
	CSRFHash           []byte
}

// AuthorizationCodeOwner is a preliminary identity observation. The caller
// must acquire its logical identity guard and recheck account authority in the
// transaction before issuing credentials.
type AuthorizationCodeOwner struct {
	AccountID, Subject string
	Generation         int64
}

// AuthorizationTransaction provides complete-operation persistence intent for
// native authorization. All mutations participate in the caller-owned outer
// transaction; no method commits independently.
type AuthorizationTransaction interface {
	Transaction
	AuthorizationCodeOwner(context.Context, [32]byte) (AuthorizationCodeOwner, error)
	SubjectForAccount(context.Context, string) (string, error)
	LockAccountAuthorization(context.Context, string) (AccountAuthorization, error)
	LockPortalSessionForConsent(context.Context, [32]byte, uint64, string, int64) (PortalSessionForConsent, error)
	PortalSessionForAuthorization(context.Context, [32]byte, uint64) (PortalAuthorizationSession, error)
	TouchPortalSession(context.Context, [32]byte, uint64, string, int64) (bool, error)
	CountActiveLoginGrants(context.Context, string, int64) (int, error)
	RecordCredentialAudit(context.Context, string, string, string) error
	StoreAuthorizationCode(context.Context, [32]byte, AuthorizationCode) error
	LoadAuthorizationCode(context.Context, [32]byte) (AuthorizationCode, error)
	ConsumeAuthorizationCode(context.Context, [32]byte) error
	CreateLoginGrant(context.Context, LoginGrant) error
	StoreRelayAccessToken(context.Context, [32]byte, string, time.Time) error
	DeleteRelayAccessToken(context.Context, [32]byte) error
	CurrentRefreshGeneration(context.Context, string) (int64, error)
	StoreRefreshGeneration(context.Context, [32]byte, string, int64) error
}

// AuthorizationOperation declares the persistence needed by native
// authorization flows. It retains admission from Start through Close while
// allowing the workflow to begin its own authoritative transaction.
type AuthorizationOperation interface {
	Context() context.Context
	Close()
	BeginAuthorization(context.Context) (AuthorizationTransaction, error)
}

// AuthorizationWork admits complete native authorization operations.
type AuthorizationWork interface {
	StartAuthorization(context.Context) (AuthorizationOperation, error)
}

// PortalAuthorizationSession includes the account fields required to authorize
// a browser request and refresh the short-lived Portal Session idle deadline.
type PortalAuthorizationSession struct {
	AccountID, Status, Email, Subject string
	Generation                        int64
	PepperVersion                     uint64
	CSRFHash                          []byte
	EmailVerified                     bool
}

// LoginGrant is the initial persistent grant established by an authorization
// code redemption.
type LoginGrant struct {
	ID, AccountID, ClientType string
	Generation                int64
}
