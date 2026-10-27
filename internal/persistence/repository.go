// Package persistence defines storage-neutral contracts used by functional
// modules. Implementations own transactions, SQL and database resource limits.
package persistence

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidCredential    = errors.New("invalid relay access credential")
	ErrNotFound             = errors.New("persistence record not found")
	ErrUnavailable          = errors.New("persistence temporarily unavailable")
	ErrWorkUnavailable      = errors.New("persistence work admission unavailable")
	ErrUncertainCommit      = errors.New("persistence commit outcome uncertain")
	ErrRecentAuthentication = errors.New("recent authentication required")
	ErrCapacityExceeded     = errors.New("persistence capacity exceeded")
)

// AccessToken is the typed Fosite session associated with a Relay Access Token.
type AccessToken struct {
	AccountID  string
	GrantID    string
	Generation int64
	ClientType string
	ExpiresAt  time.Time
}

// RelayCredential is the authoritative account and plan projection used when
// admitting a Relay Session. DatabaseNow is returned with the projection so
// expiry is evaluated against database time.
type RelayCredential struct {
	AccessToken
	AccountStatus string
	ReviewHold    bool
	SessionLimit  int
	DatabaseNow   time.Time
}

// Operation admits one complete persistence operation. It reserves shared
// work capacity, but no physical database connection, until Begin is called.
type Operation interface {
	Context() context.Context
	Begin(context.Context) (Transaction, error)
	Close()
}

// Transaction contains only the reads needed for access-token validation.
// The operation owner decides when to commit; repositories never commit.
type Transaction interface {
	AccessToken(context.Context, [32]byte) (AccessToken, error)
	RelayCredential(context.Context, [32]byte, string, string) (RelayCredential, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}

// Work admits operations and creates their later transaction scopes.
type Work interface {
	Start(context.Context) (Operation, error)
}
