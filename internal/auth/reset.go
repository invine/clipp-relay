package auth

import (
	"context"
	"crypto/sha256"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNativeIssuerResetRequired = errors.New("native OAuth credentials require an explicit reset")

func nativeKeyID(secret []byte) ([32]byte, error) {
	if len(secret) != 32 {
		return [32]byte{}, errors.New("native OAuth signing key must contain 32 bytes")
	}
	return sha256.Sum256(secret), nil
}

// ResetNativeOAuthCredentials invalidates every issued native OAuth credential
// and records the current signing identity in one transaction. Repeating an
// uncertain call after its commit succeeded is a no-op, so it cannot erase
// credentials issued after the reset.
func ResetNativeOAuthCredentials(ctx context.Context, pool *pgxpool.Pool, secret []byte) (bool, error) {
	return resetNativeOAuthCredentials(ctx, pool, secret, func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) })
}

// ResetNativeOAuthCredentialsWithCommitHookForTest injects an uncertain commit
// acknowledgement into the disposable PostgreSQL recovery test.
func ResetNativeOAuthCredentialsWithCommitHookForTest(ctx context.Context, pool *pgxpool.Pool, secret []byte, commit func(context.Context, pgx.Tx) error) (bool, error) {
	if commit == nil {
		return false, errors.New("reset commit hook is required")
	}
	return resetNativeOAuthCredentials(ctx, pool, secret, commit)
}

func resetNativeOAuthCredentials(ctx context.Context, pool *pgxpool.Pool, secret []byte, commit func(context.Context, pgx.Tx) error) (bool, error) {
	keyID, err := nativeKeyID(secret)
	if err != nil {
		return false, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(812734650219)`); err != nil {
		return false, err
	}
	var current []byte
	err = tx.QueryRow(ctx, `SELECT key_id FROM public.oauth_issuer_state WHERE singleton=true FOR UPDATE`).Scan(&current)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if err == nil && len(current) == len(keyID) && string(current) == string(keyID[:]) {
		if err = commit(ctx, tx); err != nil {
			return false, err
		}
		return false, nil
	}
	for _, table := range []string{"native_access_tokens", "native_refresh_generations", "native_authorization_codes", "native_login_grants", "relay_access_tokens", "refresh_generations", "authorization_codes", "login_grants"} {
		if _, err = tx.Exec(ctx, "DELETE FROM public."+table); err != nil {
			return false, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.oauth_issuer_state(singleton,key_id,reset_completed_at)
VALUES(true,$1,clock_timestamp()) ON CONFLICT(singleton) DO UPDATE SET key_id=EXCLUDED.key_id,reset_completed_at=EXCLUDED.reset_completed_at`, keyID[:]); err != nil {
		return false, err
	}
	id, err := uuid()
	if err != nil {
		return false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event) VALUES($1,clock_timestamp(),'credential_reset')`, id); err != nil {
		return false, err
	}
	if err = commit(ctx, tx); err != nil {
		return false, err
	}
	return true, nil
}

// VerifyNativeOAuthKey fails serving startup until the explicitly provisioned
// key matches the last completed reset. Serving never performs a reset itself.
func VerifyNativeOAuthKey(ctx context.Context, pool *pgxpool.Pool, secret []byte) error {
	keyID, err := nativeKeyID(secret)
	if err != nil {
		return err
	}
	var current []byte
	if err := pool.QueryRow(ctx, `SELECT key_id FROM public.oauth_issuer_state WHERE singleton=true`).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNativeIssuerResetRequired
		}
		return err
	}
	if len(current) != len(keyID) || string(current) != string(keyID[:]) {
		return ErrNativeIssuerResetRequired
	}
	return nil
}
