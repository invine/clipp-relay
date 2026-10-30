package postgres

import (
	"context"
	"errors"
	"time"

	"clipp-relay/internal/persistence"
	"github.com/jackc/pgx/v5"
)

var (
	_ persistence.AccountRegistrationTransaction = (*transaction)(nil)
	_ persistence.PortalSessionTransaction       = (*transaction)(nil)
)

func (t *transaction) UpsertRelayAccount(ctx context.Context, input persistence.AccountRegistration) (persistence.RegisteredAccount, error) {
	var account persistence.RegisteredAccount
	err := t.tx.QueryRow(ctx, `INSERT INTO public.accounts
		(id,issuer,subject,email,email_verified,hosted_domain,validated_at,created_at,last_portal_login_at)
		VALUES ($1,$2,$3,$4,$5,$6,clock_timestamp(),clock_timestamp(),clock_timestamp())
		ON CONFLICT (issuer,subject) DO UPDATE SET
		email=EXCLUDED.email,email_verified=EXCLUDED.email_verified,hosted_domain=EXCLUDED.hosted_domain,
		validated_at=clock_timestamp(),last_portal_login_at=clock_timestamp()
		WHERE accounts.deletion_started_at IS NULL
		AND (accounts.status<>'Pending' OR accounts.last_portal_login_at>=clock_timestamp()-interval '90 days')
		RETURNING id::text,credential_generation,(xmax=0)`,
		input.ID, input.Issuer, input.Subject, input.Email, input.EmailVerified, input.HostedDomain).
		Scan(&account.ID, &account.Generation, &account.Created)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return persistence.RegisteredAccount{}, persistence.ErrUnavailable
		}
		return persistence.RegisteredAccount{}, unavailable(err)
	}
	return account, nil
}

func (t *transaction) AccountStateAfterUncertain(ctx context.Context, accountID, deletionOperationID string) (persistence.AccountStateAfterUncertain, error) {
	var state persistence.AccountStateAfterUncertain
	err := t.tx.QueryRow(ctx, `SELECT revision,credential_generation,status,deletion_started_at IS NOT NULL
		FROM public.accounts WHERE id=$1 FOR UPDATE`, accountID).
		Scan(&state.Revision, &state.Generation, &state.Status, &state.DeletionStarted)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.AccountStateAfterUncertain{}, persistence.ErrUnavailable
	}
	if err != nil {
		return persistence.AccountStateAfterUncertain{}, unavailable(err)
	}
	if deletionOperationID != "" {
		if err = t.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.deletion_operations WHERE id=$1 AND account_id=$2)`, deletionOperationID, accountID).Scan(&state.DeletionOperationPresent); err != nil {
			return persistence.AccountStateAfterUncertain{}, unavailable(err)
		}
	}
	return state, nil
}

func (t *transaction) RecordAccountCreated(ctx context.Context, auditID, accountID string) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id)
		VALUES($1,clock_timestamp(),'account_created',$2)`, auditID, accountID)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (t *transaction) CurrentRetainedUsageWeek(ctx context.Context) (week time.Time, err error) {
	err = t.tx.QueryRow(ctx, `SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&week)
	if err != nil {
		return time.Time{}, unavailable(err)
	}
	return week, nil
}

func (t *transaction) RetainedUsageVersions(ctx context.Context) ([]uint64, error) {
	rows, err := t.tx.Query(ctx, `SELECT DISTINCT pepper_version FROM public.retained_quota_usage
		WHERE week_start=date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`)
	if err != nil {
		return nil, unavailable(err)
	}
	defer rows.Close()
	versions := make([]uint64, 0)
	for rows.Next() {
		var version uint64
		if err = rows.Scan(&version); err != nil {
			return nil, unavailable(err)
		}
		versions = append(versions, version)
	}
	if err = rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return versions, nil
}

func (t *transaction) RetainedUsageForIdentity(ctx context.Context, version uint64, digest [32]byte, week time.Time) (persistence.RetainedUsageRecord, bool, error) {
	var record persistence.RetainedUsageRecord
	record.PepperVersion = version
	record.IdentityDigest = digest
	record.Week = week
	err := t.tx.QueryRow(ctx, `SELECT recorded_bytes FROM public.retained_quota_usage
		WHERE pepper_version=$1 AND identity_digest=$2 AND week_start=$3 FOR UPDATE`, version, digest[:], week).
		Scan(&record.RecordedBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.RetainedUsageRecord{}, false, nil
	}
	if err != nil {
		return persistence.RetainedUsageRecord{}, false, unavailable(err)
	}
	return record, true, nil
}

func (t *transaction) ImportRetainedUsage(ctx context.Context, accountID string, record persistence.RetainedUsageRecord) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO public.weekly_relay_usage
		(account_id,week_start,recorded_bytes,sequence,latest_operation_id,latest_delta_bytes)
		VALUES($1,$2,$3,0,NULL,0)`, accountID, record.Week, record.RecordedBytes)
	if err != nil {
		return unavailable(err)
	}
	tag, err := t.tx.Exec(ctx, `DELETE FROM public.retained_quota_usage
		WHERE pepper_version=$1 AND identity_digest=$2 AND week_start=$3`, record.PepperVersion, record.IdentityDigest[:], record.Week)
	if err != nil {
		return unavailable(err)
	}
	if tag.RowsAffected() != 1 {
		return persistence.ErrInvalidCredential
	}
	return nil
}

func (t *transaction) CreatePortalSession(ctx context.Context, digest [32]byte, version uint64, accountID string, generation int64, csrfDigest [32]byte) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO public.portal_sessions
		(credential_digest,pepper_version,account_id,credential_generation,csrf_digest,google_authenticated_at,created_at,last_used_at,idle_expires_at,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,clock_timestamp(),clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '12 hours')`,
		digest[:], version, accountID, generation, csrfDigest[:])
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (t *transaction) PortalSessionForOwner(ctx context.Context, digest [32]byte, version uint64) (persistence.PortalSessionOwner, error) {
	var session persistence.PortalSessionOwner
	err := t.tx.QueryRow(ctx, `SELECT a.id::text,a.status,a.email,a.subject,a.credential_generation,ps.pepper_version,ps.csrf_digest
		FROM public.portal_sessions ps JOIN public.accounts a ON a.id=ps.account_id
		WHERE ps.credential_digest=$1 AND ps.pepper_version=$2
		AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp()
		AND ps.credential_generation=a.credential_generation
		AND (a.status<>'Pending' OR a.last_portal_login_at>=clock_timestamp()-interval '90 days')`, digest[:], version).
		Scan(&session.AccountID, &session.Status, &session.Email, &session.Subject, &session.Generation, &session.PepperVersion, &session.CSRFHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.PortalSessionOwner{}, persistence.ErrInvalidCredential
	}
	if err != nil {
		return persistence.PortalSessionOwner{}, unavailable(err)
	}
	return session, nil
}

func (t *transaction) DeletePortalSession(ctx context.Context, digest [32]byte, version uint64) (bool, error) {
	tag, err := t.tx.Exec(ctx, `DELETE FROM public.portal_sessions WHERE credential_digest=$1 AND pepper_version=$2`, digest[:], version)
	if err != nil {
		return false, unavailable(err)
	}
	return tag.RowsAffected() == 1, nil
}

var _ persistence.AccountTransaction = (*transaction)(nil)
