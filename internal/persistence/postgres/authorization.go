package postgres

import (
	"context"
	"errors"
	"time"

	"clipp-relay/internal/persistence"
	"github.com/jackc/pgx/v5"
)

var _ persistence.AuthorizationTransaction = (*transaction)(nil)

func (t *transaction) AuthorizationCodeOwner(ctx context.Context, digest [32]byte) (persistence.AuthorizationCodeOwner, error) {
	var owner persistence.AuthorizationCodeOwner
	err := t.tx.QueryRow(ctx, `SELECT c.account_id::text,a.subject,c.credential_generation
		FROM public.native_authorization_codes c JOIN public.accounts a ON a.id=c.account_id
		WHERE c.credential_digest=$1 AND c.consumed_at IS NULL AND c.expires_at>clock_timestamp()`, digest[:]).
		Scan(&owner.AccountID, &owner.Subject, &owner.Generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.AuthorizationCodeOwner{}, persistence.ErrInvalidCredential
	}
	if err != nil {
		return persistence.AuthorizationCodeOwner{}, unavailable(err)
	}
	return owner, nil
}

func (t *transaction) SubjectForAccount(ctx context.Context, accountID string) (string, error) {
	var subject string
	err := t.tx.QueryRow(ctx, `SELECT subject FROM public.accounts WHERE id=$1`, accountID).Scan(&subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", persistence.ErrInvalidCredential
	}
	if err != nil {
		return "", unavailable(err)
	}
	return subject, nil
}

func (t *transaction) LockAccountAuthorization(ctx context.Context, accountID string) (persistence.AccountAuthorization, error) {
	var account persistence.AccountAuthorization
	err := t.tx.QueryRow(ctx, `SELECT status,credential_generation FROM public.accounts WHERE id=$1 AND NOT recovery_review_hold FOR UPDATE`, accountID).Scan(&account.Status, &account.Generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.AccountAuthorization{}, persistence.ErrInvalidCredential
	}
	if err != nil {
		return persistence.AccountAuthorization{}, unavailable(err)
	}
	return account, nil
}

func (t *transaction) LockPortalSessionForConsent(ctx context.Context, digest [32]byte, pepperVersion uint64, accountID string, generation int64) (persistence.PortalSessionForConsent, error) {
	var session persistence.PortalSessionForConsent
	err := t.tx.QueryRow(ctx, `SELECT credential_generation,csrf_digest FROM public.portal_sessions
		WHERE credential_digest=$1 AND pepper_version=$2 AND account_id=$3 AND credential_generation=$4
		AND idle_expires_at>clock_timestamp() AND absolute_expires_at>clock_timestamp() FOR UPDATE`, digest[:], pepperVersion, accountID, generation).
		Scan(&session.Generation, &session.CSRFHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.PortalSessionForConsent{}, persistence.ErrInvalidCredential
	}
	if err != nil {
		return persistence.PortalSessionForConsent{}, unavailable(err)
	}
	return session, nil
}

func (t *transaction) PortalSessionForAuthorization(ctx context.Context, digest [32]byte, pepperVersion uint64) (persistence.PortalAuthorizationSession, error) {
	var session persistence.PortalAuthorizationSession
	err := t.tx.QueryRow(ctx, `SELECT a.id::text,a.subject,a.credential_generation,ps.pepper_version,ps.csrf_digest
		FROM public.portal_sessions ps JOIN public.accounts a ON a.id=ps.account_id
		WHERE ps.credential_digest=$1 AND ps.pepper_version=$2 AND ps.idle_expires_at>clock_timestamp()
		AND ps.absolute_expires_at>clock_timestamp() AND ps.credential_generation=a.credential_generation
		AND (a.status<>'Pending' OR a.last_portal_login_at>=clock_timestamp()-interval '90 days')`, digest[:], pepperVersion).
		Scan(&session.AccountID, &session.Subject, &session.Generation, &session.PepperVersion, &session.CSRFHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.PortalAuthorizationSession{}, persistence.ErrInvalidCredential
	}
	if err != nil {
		return persistence.PortalAuthorizationSession{}, unavailable(err)
	}
	return session, nil
}

func (t *transaction) TouchPortalSession(ctx context.Context, digest [32]byte, pepperVersion uint64, accountID string, generation int64) (bool, error) {
	var touched bool
	err := t.tx.QueryRow(ctx, `UPDATE public.portal_sessions ps SET last_used_at=clock_timestamp(),
		idle_expires_at=LEAST(ps.absolute_expires_at,clock_timestamp()+interval '30 minutes')
		WHERE ps.credential_digest=$1 AND ps.pepper_version=$2 AND ps.account_id=$3
		AND ps.credential_generation=$4 AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp()
		AND EXISTS (SELECT 1 FROM public.accounts a WHERE a.id=ps.account_id AND a.credential_generation=ps.credential_generation
		AND (a.status<>'Pending' OR a.last_portal_login_at>=clock_timestamp()-interval '90 days')) RETURNING true`, digest[:], pepperVersion, accountID, generation).Scan(&touched)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, unavailable(err)
	}
	return touched, nil
}

func (t *transaction) CountActiveLoginGrants(ctx context.Context, accountID string, generation int64) (int, error) {
	var count int
	err := t.tx.QueryRow(ctx, `SELECT count(*) FROM public.native_login_grants WHERE account_id=$1 AND credential_generation=$2
		AND terminated_at IS NULL AND idle_expires_at>clock_timestamp() AND absolute_expires_at>clock_timestamp()`, accountID, generation).Scan(&count)
	if err != nil {
		return 0, unavailable(err)
	}
	return count, nil
}

func (t *transaction) RecordCredentialAudit(ctx context.Context, event, accountID, clientType string) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id,client_type)
		VALUES(gen_random_uuid(),clock_timestamp(),$1,$2,$3)`, event, accountID, clientType)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (t *transaction) StoreAuthorizationCode(ctx context.Context, digest [32]byte, code persistence.AuthorizationCode) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO public.native_authorization_codes(credential_digest,account_id,credential_generation,client_type,redirect_uri,code_challenge,grant_id,issued_at,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,clock_timestamp(),$8)`, digest[:], code.AccountID, code.Generation, code.ClientType, code.RedirectURI, code.Challenge, code.GrantID, code.ExpiresAt)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (t *transaction) LoadAuthorizationCode(ctx context.Context, digest [32]byte) (persistence.AuthorizationCode, error) {
	var code persistence.AuthorizationCode
	err := t.tx.QueryRow(ctx, `SELECT account_id::text,credential_generation,client_type,redirect_uri,code_challenge,grant_id::text,consumed_at IS NOT NULL,expires_at
		FROM public.native_authorization_codes WHERE credential_digest=$1 AND expires_at>clock_timestamp() FOR UPDATE`, digest[:]).
		Scan(&code.AccountID, &code.Generation, &code.ClientType, &code.RedirectURI, &code.Challenge, &code.GrantID, &code.Consumed, &code.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.AuthorizationCode{}, persistence.ErrInvalidCredential
	}
	if err != nil {
		return persistence.AuthorizationCode{}, unavailable(err)
	}
	return code, nil
}

func (t *transaction) ConsumeAuthorizationCode(ctx context.Context, digest [32]byte) error {
	tag, err := t.tx.Exec(ctx, `UPDATE public.native_authorization_codes SET consumed_at=clock_timestamp()
		WHERE credential_digest=$1 AND consumed_at IS NULL AND expires_at>clock_timestamp()`, digest[:])
	if err != nil {
		return unavailable(err)
	}
	if tag.RowsAffected() != 1 {
		return persistence.ErrInvalidCredential
	}
	return nil
}

func (t *transaction) CreateLoginGrant(ctx context.Context, grant persistence.LoginGrant) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO public.native_login_grants(id,account_id,credential_generation,client_type,current_refresh_generation,created_at,last_used_at,idle_expires_at,absolute_expires_at)
		VALUES($1,$2,$3,$4,1,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 days',clock_timestamp()+interval '180 days')`, grant.ID, grant.AccountID, grant.Generation, grant.ClientType)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (t *transaction) StoreRelayAccessToken(ctx context.Context, digest [32]byte, grantID string, expiresAt time.Time) error {
	tag, err := t.tx.Exec(ctx, `INSERT INTO public.native_access_tokens(credential_digest,grant_id,issued_at,expires_at)
		SELECT $1,$2,clock_timestamp(),$3 FROM public.native_login_grants g JOIN public.accounts a ON a.id=g.account_id
		WHERE g.id=$2 AND g.terminated_at IS NULL AND g.idle_expires_at>clock_timestamp() AND g.absolute_expires_at>clock_timestamp()
		AND a.status='Active' AND NOT a.recovery_review_hold AND a.credential_generation=g.credential_generation`, digest[:], grantID, expiresAt)
	if err != nil {
		return unavailable(err)
	}
	if tag.RowsAffected() != 1 {
		return persistence.ErrInvalidCredential
	}
	return nil
}

func (t *transaction) DeleteRelayAccessToken(ctx context.Context, digest [32]byte) error {
	_, err := t.tx.Exec(ctx, `DELETE FROM public.native_access_tokens WHERE credential_digest=$1`, digest[:])
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (t *transaction) CurrentRefreshGeneration(ctx context.Context, grantID string) (int64, error) {
	var generation int64
	err := t.tx.QueryRow(ctx, `SELECT current_refresh_generation FROM public.native_login_grants WHERE id=$1 FOR UPDATE`, grantID).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, persistence.ErrInvalidCredential
	}
	if err != nil {
		return 0, unavailable(err)
	}
	return generation, nil
}

func (t *transaction) StoreRefreshGeneration(ctx context.Context, digest [32]byte, grantID string, generation int64) error {
	_, err := t.tx.Exec(ctx, `INSERT INTO public.native_refresh_generations(credential_digest,grant_id,generation,issued_at) VALUES($1,$2,$3,clock_timestamp())`, digest[:], grantID, generation)
	if err != nil {
		return unavailable(err)
	}
	return nil
}
