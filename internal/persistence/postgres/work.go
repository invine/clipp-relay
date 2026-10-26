// Package postgres adapts PostgreSQL and the shared database runtime to the
// storage-neutral persistence contracts.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"github.com/jackc/pgx/v5"
)

type Work struct{ runtime *database.Runtime }

func New(runtime *database.Runtime) *Work { return &Work{runtime: runtime} }

func (w *Work) Start(parent context.Context) (persistence.Operation, error) {
	ctx, release, err := w.runtime.StartUnit(parent)
	if err != nil {
		return nil, unavailable(err)
	}
	return &operation{runtime: w.runtime, ctx: ctx, release: release}, nil
}

type operation struct {
	runtime *database.Runtime
	ctx     context.Context
	release func()
}

func (o *operation) Context() context.Context { return o.ctx }
func (o *operation) Close()                   { o.release() }
func (o *operation) Begin(ctx context.Context) (persistence.Transaction, error) {
	if err := o.ctx.Err(); err != nil {
		return nil, unavailable(err)
	}
	if err := ctx.Err(); err != nil {
		return nil, unavailable(err)
	}
	bounded, finish := operationContext(o.ctx, ctx)
	tx, err := o.runtime.Begin(bounded)
	if err != nil {
		finish()
		return nil, unavailable(err)
	}
	return &transaction{tx: tx, finish: finish}, nil
}

func operationContext(operation, caller context.Context) (context.Context, func()) {
	var ctx context.Context
	var cancel context.CancelFunc
	if deadline, ok := caller.Deadline(); ok {
		ctx, cancel = context.WithDeadline(operation, deadline)
	} else {
		ctx, cancel = context.WithCancel(operation)
	}
	stop := context.AfterFunc(caller, cancel)
	if caller.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

type transaction struct {
	tx     pgx.Tx
	finish func()
}

func (t *transaction) AccessToken(ctx context.Context, digest [32]byte) (persistence.AccessToken, error) {
	var token persistence.AccessToken
	err := t.tx.QueryRow(ctx, `SELECT g.account_id::text,g.id::text,g.credential_generation,g.client_type,t.expires_at
		FROM public.native_access_tokens t
		JOIN public.native_login_grants g ON g.id=t.grant_id
		JOIN public.accounts a ON a.id=g.account_id
		WHERE t.credential_digest=$1 AND t.expires_at>clock_timestamp()
		AND g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp()
		AND g.absolute_expires_at>clock_timestamp() AND a.status='Active'
		AND NOT a.recovery_review_hold AND a.credential_generation=g.credential_generation`, digest[:]).
		Scan(&token.AccountID, &token.GrantID, &token.Generation, &token.ClientType, &token.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.AccessToken{}, persistence.ErrInvalidCredential
	}
	if err != nil {
		return persistence.AccessToken{}, unavailable(err)
	}
	return token, nil
}

func (t *transaction) RelayCredential(ctx context.Context, digest [32]byte, grantID, accountID string) (persistence.RelayCredential, error) {
	var result persistence.RelayCredential
	err := t.tx.QueryRow(ctx, `SELECT g.account_id::text,g.id::text,g.credential_generation,g.client_type,
		t.expires_at,a.status,a.recovery_review_hold,COALESCE(a.sessions_override,p.sessions),clock_timestamp()
		FROM public.native_access_tokens t
		JOIN public.native_login_grants g ON g.id=t.grant_id
		JOIN public.accounts a ON a.id=g.account_id
		JOIN public.quota_plans p ON p.id=a.plan_id
		WHERE t.credential_digest=$1 AND g.id=$2 AND g.account_id=$3
		AND t.expires_at>clock_timestamp() AND g.terminated_at IS NULL
		AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp()
		AND NOT a.recovery_review_hold AND a.status='Active'
		AND g.credential_generation=a.credential_generation`, digest[:], grantID, accountID).
		Scan(&result.AccountID, &result.GrantID, &result.Generation, &result.ClientType, &result.ExpiresAt,
			&result.AccountStatus, &result.ReviewHold, &result.SessionLimit, &result.DatabaseNow)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.RelayCredential{}, persistence.ErrInvalidCredential
	}
	if err != nil {
		return persistence.RelayCredential{}, unavailable(err)
	}
	return result, nil
}

func (t *transaction) Commit(ctx context.Context) error {
	defer t.finish()
	err := t.tx.Commit(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("%w: %w", persistence.ErrUncertainCommit, err)
		}
		return persistence.ErrUncertainCommit
	}
	return nil
}
func (t *transaction) Rollback(ctx context.Context) error {
	defer t.finish()
	if err := t.tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return unavailable(err)
	}
	return nil
}

func unavailable(err error) error {
	if err == nil {
		return persistence.ErrUnavailable
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(persistence.ErrUnavailable, err)
	}
	return persistence.ErrUnavailable
}
