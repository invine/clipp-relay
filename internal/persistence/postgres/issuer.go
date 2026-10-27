package postgres

import (
	"context"
	"crypto/subtle"
	"errors"

	"clipp-relay/internal/persistence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IssuerMaintenance uses the explicitly selected migration/startup pool. It is
// never composed into the serving Auth handler.
type IssuerMaintenance struct{ pool *pgxpool.Pool }

func NewIssuerMaintenance(pool *pgxpool.Pool) *IssuerMaintenance {
	return &IssuerMaintenance{pool: pool}
}
func (m *IssuerMaintenance) BeginIssuerReset(ctx context.Context) (persistence.IssuerResetTransaction, error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return nil, persistence.ErrUnavailable
	}
	return &issuerResetTx{tx: tx}, nil
}
func (m *IssuerMaintenance) VerifyIssuerKey(ctx context.Context, keyID []byte) (bool, error) {
	var current []byte
	err := m.pool.QueryRow(ctx, `SELECT key_id FROM public.oauth_issuer_state WHERE singleton=true`).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, persistence.ErrUnavailable
	}
	if len(current) != len(keyID) {
		return false, nil
	}
	return subtle.ConstantTimeCompare(current, keyID) == 1, nil
}

type issuerResetTx struct{ tx pgx.Tx }

func (t *issuerResetTx) LockIssuerReset(ctx context.Context) error {
	if _, err := t.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(812734650219)`); err != nil {
		return issuerUnavailable(err)
	}
	return nil
}
func (t *issuerResetTx) IssuerKeyID(ctx context.Context) ([]byte, bool, error) {
	var key []byte
	err := t.tx.QueryRow(ctx, `SELECT key_id FROM public.oauth_issuer_state WHERE singleton=true FOR UPDATE`).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, issuerUnavailable(err)
	}
	return key, true, nil
}
func (t *issuerResetTx) ResetNativeIssuerCredentials(ctx context.Context) error {
	for _, table := range []string{"native_access_tokens", "native_refresh_generations", "native_authorization_codes", "native_login_grants", "relay_access_tokens", "refresh_generations", "authorization_codes", "login_grants"} {
		if _, err := t.tx.Exec(ctx, "DELETE FROM public."+table); err != nil {
			return issuerUnavailable(err)
		}
	}
	return nil
}
func (t *issuerResetTx) RecordIssuerReset(ctx context.Context, keyID []byte, auditID string) error {
	if _, err := t.tx.Exec(ctx, `INSERT INTO public.oauth_issuer_state(singleton,key_id,reset_completed_at) VALUES(true,$1,clock_timestamp()) ON CONFLICT(singleton) DO UPDATE SET key_id=EXCLUDED.key_id,reset_completed_at=EXCLUDED.reset_completed_at`, keyID); err != nil {
		return issuerUnavailable(err)
	}
	if _, err := t.tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event) VALUES($1,clock_timestamp(),'credential_reset')`, auditID); err != nil {
		return issuerUnavailable(err)
	}
	return nil
}
func (t *issuerResetTx) Commit(ctx context.Context) error {
	if err := t.tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return persistence.ErrKnownRollback
		}
		return persistence.ErrUncertainCommit
	}
	return nil
}
func (t *issuerResetTx) Rollback(ctx context.Context) error {
	if err := t.tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return issuerUnavailable(err)
	}
	return nil
}
func issuerUnavailable(error) error { return persistence.ErrUnavailable }

var _ persistence.IssuerMaintenance = (*IssuerMaintenance)(nil)
var _ persistence.IssuerResetTransaction = (*issuerResetTx)(nil)
