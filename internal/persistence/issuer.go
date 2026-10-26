package persistence

import "context"

type IssuerResetTransaction interface {
	Commit(context.Context) error
	Rollback(context.Context) error
	LockIssuerReset(context.Context) error
	IssuerKeyID(context.Context) ([]byte, bool, error)
	ResetNativeIssuerCredentials(context.Context) error
	RecordIssuerReset(context.Context, []byte, string) error
}

type IssuerMaintenance interface {
	BeginIssuerReset(context.Context) (IssuerResetTransaction, error)
	VerifyIssuerKey(context.Context, []byte) (bool, error)
}
