package persistence

import (
	"context"
	"time"
)

// RefreshOwner is the preliminary identity observation for a presented refresh
// credential. Callers acquire the account identity guard, then recheck authority.
type RefreshOwner struct {
	AccountID, Subject, GrantID string
	Generation                  int64
}

// RefreshCredential is the locked refresh row and grant projection used by
// Fosite. Expiry is evaluated by the database adapter.
type RefreshCredential struct {
	AccountID, GrantID, ClientType   string
	AccountGeneration, Generation    int64
	Consumed, Active                 bool
	IdleExpiresAt, AbsoluteExpiresAt time.Time
}

// RefreshTransaction owns every refresh read and write in one caller-owned
// transaction. Confirmation is a separate transaction on the admitted operation.
type RefreshTransaction interface {
	AuthorizationTransaction
	RefreshOwner(context.Context, [32]byte) (RefreshOwner, error)
	LoadRefreshCredential(context.Context, [32]byte) (RefreshCredential, error)
	RotateRefreshCredential(context.Context, [32]byte, string) error
	RevokeRefreshGrant(context.Context, string) (string, error)
	DeleteGrantAccessTokens(context.Context, string) error
	ConfirmRefreshGrantRevoked(context.Context, string) (bool, error)
}
