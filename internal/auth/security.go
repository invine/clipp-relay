package auth

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// revokeAccountCredentials runs under the account row lock in the caller's
// mutation transaction. Generation fencing makes old codes and Access Tokens
// invalid; explicit termination also ends grants and all Portal Sessions.
func revokeAccountCredentials(ctx context.Context, tx pgx.Tx, accountID string) error {
	if _, err := tx.Exec(ctx, `UPDATE public.accounts SET credential_generation=credential_generation+1 WHERE id=$1`, accountID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE public.login_grants SET terminated_at=clock_timestamp() WHERE account_id=$1 AND terminated_at IS NULL`, accountID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM public.portal_sessions WHERE account_id=$1`, accountID)
	return err
}
