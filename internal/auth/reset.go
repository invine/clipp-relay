package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"clipp-relay/internal/persistence"
)

var ErrNativeIssuerResetRequired = errors.New("native OAuth credentials require an explicit reset")

func nativeKeyID(secret []byte) ([32]byte, error) {
	if len(secret) != 32 {
		return [32]byte{}, errors.New("native OAuth signing key must contain 32 bytes")
	}
	return sha256.Sum256(secret), nil
}

// ResetNativeOAuthCredentials invalidates every issued native credential only
// when the selected issuer key differs from the last committed reset.
func ResetNativeOAuthCredentials(ctx context.Context, store persistence.IssuerMaintenance, secret []byte) (bool, error) {
	return resetNativeOAuthCredentials(ctx, store, secret, func(ctx context.Context, tx persistence.IssuerResetTransaction) error { return tx.Commit(ctx) })
}

// ResetNativeOAuthCredentialsWithCommitHookForTest injects an uncertain commit
// acknowledgement without exposing driver types to Auth.
func ResetNativeOAuthCredentialsWithCommitHookForTest(ctx context.Context, store persistence.IssuerMaintenance, secret []byte, commit func(context.Context, persistence.IssuerResetTransaction) error) (bool, error) {
	if commit == nil {
		return false, errors.New("reset commit hook is required")
	}
	return resetNativeOAuthCredentials(ctx, store, secret, commit)
}

func resetNativeOAuthCredentials(ctx context.Context, store persistence.IssuerMaintenance, secret []byte, commit func(context.Context, persistence.IssuerResetTransaction) error) (bool, error) {
	keyID, err := nativeKeyID(secret)
	if err != nil {
		return false, err
	}
	tx, err := store.BeginIssuerReset(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = tx.LockIssuerReset(ctx); err != nil {
		return false, err
	}
	current, exists, err := tx.IssuerKeyID(ctx)
	if err != nil {
		return false, err
	}
	if exists && len(current) == len(keyID) && subtle.ConstantTimeCompare(current, keyID[:]) == 1 {
		return false, commit(ctx, tx)
	}
	if err = tx.ResetNativeIssuerCredentials(ctx); err != nil {
		return false, err
	}
	auditID, err := uuid()
	if err != nil {
		return false, err
	}
	if err = tx.RecordIssuerReset(ctx, keyID[:], auditID); err != nil {
		return false, err
	}
	if err = commit(ctx, tx); err == nil {
		return true, nil
	}
	if errors.Is(err, persistence.ErrKnownRollback) {
		return false, err
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	confirmed, checkErr := store.VerifyIssuerKey(checkCtx, keyID[:])
	if checkErr == nil && confirmed {
		return true, nil
	}
	if checkErr != nil {
		return false, fmt.Errorf("issuer reset outcome uncertain: %w", errors.Join(err, checkErr))
	}
	return false, fmt.Errorf("issuer reset outcome uncertain: %w", err)
}

// VerifyNativeOAuthKey checks the durable reset record; serving startup never
// performs a reset.
func VerifyNativeOAuthKey(ctx context.Context, store persistence.IssuerMaintenance, secret []byte) error {
	keyID, err := nativeKeyID(secret)
	if err != nil {
		return err
	}
	matches, err := store.VerifyIssuerKey(ctx, keyID[:])
	if err != nil {
		return err
	}
	if !matches {
		return ErrNativeIssuerResetRequired
	}
	return nil
}
