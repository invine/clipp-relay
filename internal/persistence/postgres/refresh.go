package postgres

import (
	"context"
	"errors"

	"clipp-relay/internal/persistence"
	"github.com/jackc/pgx/v5"
)

var _ persistence.RefreshTransaction = (*transaction)(nil)

func (t *transaction) RefreshOwner(ctx context.Context, digest [32]byte) (persistence.RefreshOwner, error) {
	var owner persistence.RefreshOwner
	err := t.tx.QueryRow(ctx, `SELECT g.account_id::text,a.subject,g.id::text,g.credential_generation
		FROM public.native_refresh_generations r JOIN public.native_login_grants g ON g.id=r.grant_id
		JOIN public.accounts a ON a.id=g.account_id WHERE r.credential_digest=$1`, digest[:]).
		Scan(&owner.AccountID, &owner.Subject, &owner.GrantID, &owner.Generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.RefreshOwner{}, persistence.ErrInvalidCredential
	}
	if err != nil {
		return persistence.RefreshOwner{}, unavailable(err)
	}
	return owner, nil
}

func (t *transaction) LoadRefreshCredential(ctx context.Context, digest [32]byte) (persistence.RefreshCredential, error) {
	var c persistence.RefreshCredential
	err := t.tx.QueryRow(ctx, `SELECT g.account_id::text,g.id::text,g.client_type,g.credential_generation,r.generation,
		r.consumed_at IS NOT NULL,(g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp()
		AND a.status='Active' AND NOT a.recovery_review_hold AND a.credential_generation=g.credential_generation),g.idle_expires_at,g.absolute_expires_at
		FROM public.native_refresh_generations r JOIN public.native_login_grants g ON g.id=r.grant_id
		JOIN public.accounts a ON a.id=g.account_id WHERE r.credential_digest=$1 FOR UPDATE OF r,g`, digest[:]).
		Scan(&c.AccountID, &c.GrantID, &c.ClientType, &c.AccountGeneration, &c.Generation, &c.Consumed, &c.Active, &c.IdleExpiresAt, &c.AbsoluteExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.RefreshCredential{}, persistence.ErrInvalidCredential
	}
	if err != nil {
		return persistence.RefreshCredential{}, unavailable(err)
	}
	return c, nil
}

func (t *transaction) RotateRefreshCredential(ctx context.Context, digest [32]byte, grantID string) error {
	var actual string
	err := t.tx.QueryRow(ctx, `UPDATE public.native_refresh_generations SET consumed_at=clock_timestamp()
		WHERE credential_digest=$1 AND consumed_at IS NULL RETURNING grant_id::text`, digest[:]).Scan(&actual)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.ErrInvalidCredential
	}
	if err != nil {
		return unavailable(err)
	}
	if actual != grantID {
		return persistence.ErrInvalidCredential
	}
	tag, err := t.tx.Exec(ctx, `UPDATE public.native_login_grants SET current_refresh_generation=current_refresh_generation+1,
		last_used_at=clock_timestamp(),idle_expires_at=LEAST(absolute_expires_at,clock_timestamp()+interval '30 days')
		WHERE id=$1 AND terminated_at IS NULL`, grantID)
	if err != nil {
		return unavailable(err)
	}
	if tag.RowsAffected() != 1 {
		return persistence.ErrInvalidCredential
	}
	return nil
}

func (t *transaction) RevokeRefreshGrant(ctx context.Context, grantID string) (string, error) {
	var account, client string
	err := t.tx.QueryRow(ctx, `UPDATE public.native_login_grants SET terminated_at=COALESCE(terminated_at,clock_timestamp())
		WHERE id=$1 RETURNING account_id::text,client_type`, grantID).Scan(&account, &client)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", persistence.ErrInvalidCredential
	}
	if err != nil {
		return "", unavailable(err)
	}
	if err = t.RecordCredentialAudit(ctx, "refresh_reuse", account, client); err != nil {
		return "", err
	}
	return account, nil
}

func (t *transaction) DeleteGrantAccessTokens(ctx context.Context, grantID string) error {
	_, err := t.tx.Exec(ctx, `DELETE FROM public.native_access_tokens WHERE grant_id=$1`, grantID)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (t *transaction) ConfirmRefreshGrantRevoked(ctx context.Context, grantID string) (bool, error) {
	var revoked bool
	err := t.tx.QueryRow(ctx, `SELECT terminated_at IS NOT NULL FROM public.native_login_grants WHERE id=$1`, grantID).Scan(&revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, persistence.ErrInvalidCredential
	}
	if err != nil {
		return false, unavailable(err)
	}
	return revoked, nil
}
