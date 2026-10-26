package postgres

import (
	"context"
	"crypto/subtle"
	"errors"

	"clipp-relay/internal/persistence"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (t *transaction) LockAdministratorAccounts(ctx context.Context, actor, target string) error {
	rows, err := t.tx.Query(ctx, `SELECT id FROM public.accounts WHERE id=$1 OR id=$2 ORDER BY id FOR UPDATE`, actor, target)
	if err != nil {
		return unavailable(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return unavailable(err)
		}
		if id == actor {
			count++
		}
	}
	if err = rows.Err(); err != nil {
		return unavailable(err)
	}
	if count == 0 {
		return persistence.ErrNotFound
	}
	return nil
}

func (t *transaction) AdministratorSession(ctx context.Context, accountID string, digest []byte, version uint64) (persistence.AdministratorSession, error) {
	var v persistence.AdministratorSession
	err := t.tx.QueryRow(ctx, `SELECT a.email,a.email_verified,a.hosted_domain,
ps.google_authenticated_at>=clock_timestamp()-interval '10 minutes' AND ps.google_authenticated_at<=clock_timestamp()+interval '1 minute'
FROM public.accounts a JOIN public.portal_sessions ps ON ps.account_id=a.id
WHERE a.id=$1
AND ps.credential_digest=$2 AND ps.pepper_version=$3 AND ps.credential_generation=a.credential_generation
AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp()`, accountID, digest, version).Scan(&v.Email, &v.Verified, &v.HostedDomain, &v.Recent)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, persistence.ErrInvalidCredential
	}
	if err != nil {
		return v, unavailable(err)
	}
	return v, nil
}

func (t *transaction) AccountAccessState(ctx context.Context, id string, lock bool) (persistence.AccountAccessState, error) {
	var a persistence.AccountAccessState
	query := `SELECT a.id::text,a.status,a.subject,a.revision,a.credential_generation,a.deletion_started_at IS NOT NULL,COALESCE(a.weekly_bytes_override,p.weekly_bytes,0),COALESCE(a.sessions_override,p.sessions,0),a.plan_id::text,a.weekly_bytes_override,a.sessions_override FROM public.accounts a LEFT JOIN public.quota_plans p ON p.id=a.plan_id WHERE a.id=$1`
	if lock {
		query += ` FOR UPDATE OF a`
	}
	err := t.tx.QueryRow(ctx, query, id).Scan(&a.ID, &a.Status, &a.Subject, &a.Revision, &a.Generation, &a.Deleting, &a.WeeklyBytes, &a.Sessions, &a.PlanID, &a.WeeklyOverride, &a.SessionOverride)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, persistence.ErrNotFound
	}
	if err != nil {
		return a, unavailable(err)
	}
	return a, nil
}

func (t *transaction) ApplyAdminAccountMutation(ctx context.Context, m persistence.AdminAccountMutation) (persistence.AdminAccountMutationResult, error) {
	var out persistence.AdminAccountMutationResult
	a, err := t.AccountAccessState(ctx, m.AccountID, true)
	if err != nil {
		return out, err
	}
	out.Before = a
	if a.Deleting || a.Revision != m.ExpectedRevision {
		return out, persistence.ErrConflict
	}
	newStatus := a.Status
	if m.NextStatus != "" {
		newStatus = m.NextStatus
	}
	weekly, sessions := a.WeeklyBytes, a.Sessions
	if m.Action == "approve" || m.Action == "assign" {
		var archived bool
		err = t.tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL,weekly_bytes,sessions FROM public.quota_plans WHERE id=$1 FOR UPDATE`, m.PlanID).Scan(&archived, &weekly, &sessions)
		if errors.Is(err, pgx.ErrNoRows) || archived {
			return out, persistence.ErrConflict
		}
		if err != nil {
			return out, unavailable(err)
		}
		if _, err = t.tx.Exec(ctx, `UPDATE public.quota_plans SET first_assigned_at=COALESCE(first_assigned_at,clock_timestamp()) WHERE id=$1`, m.PlanID); err != nil {
			return out, unavailable(err)
		}
		if a.WeeklyOverride != nil {
			weekly = *a.WeeklyOverride
		}
		if a.SessionOverride != nil {
			sessions = *a.SessionOverride
		}
	}
	if m.Action == "override" {
		if m.WeeklyOverride != nil {
			weekly = *m.WeeklyOverride
		}
		if m.SessionOverride != nil {
			sessions = *m.SessionOverride
		}
	}
	var tag pgconn.CommandTag
	switch m.Action {
	case "recovery-review":
		tag, err = t.tx.Exec(ctx, `UPDATE public.accounts SET recovery_review_hold=false,revision=revision+1 WHERE id=$1 AND recovery_review_hold`, m.AccountID)
	case "approve", "assign":
		tag, err = t.tx.Exec(ctx, `UPDATE public.accounts SET status=$2,plan_id=$3,revision=revision+1 WHERE id=$1`, m.AccountID, newStatus, m.PlanID)
	case "override":
		tag, err = t.tx.Exec(ctx, `UPDATE public.accounts SET weekly_bytes_override=$2,sessions_override=$3,revision=revision+1 WHERE id=$1`, m.AccountID, m.WeeklyOverride, m.SessionOverride)
	default:
		tag, err = t.tx.Exec(ctx, `UPDATE public.accounts SET status=$2,revision=revision+1 WHERE id=$1`, m.AccountID, newStatus)
	}
	if err != nil {
		return out, unavailable(err)
	}
	if tag.RowsAffected() != 1 {
		return out, persistence.ErrConflict
	}
	if m.Action == "override" {
		if err = t.tx.QueryRow(ctx, `SELECT COALESCE(a.weekly_bytes_override,p.weekly_bytes,0),COALESCE(a.sessions_override,p.sessions,0) FROM public.accounts a JOIN public.quota_plans p ON p.id=a.plan_id WHERE a.id=$1`, m.AccountID).Scan(&weekly, &sessions); err != nil {
			return out, unavailable(err)
		}
	}
	if m.Security {
		if _, err = t.tx.Exec(ctx, `UPDATE public.accounts SET credential_generation=credential_generation+1 WHERE id=$1`, m.AccountID); err == nil {
			_, err = t.tx.Exec(ctx, `UPDATE public.native_login_grants SET terminated_at=clock_timestamp() WHERE account_id=$1 AND terminated_at IS NULL`, m.AccountID)
		}
		if err == nil {
			_, err = t.tx.Exec(ctx, `DELETE FROM public.portal_sessions WHERE account_id=$1`, m.AccountID)
		}
		if err != nil {
			return out, unavailable(err)
		}
	}
	var auditPlan any
	if m.Action == "approve" || m.Action == "assign" {
		auditPlan = m.PlanID
	}
	if _, err = t.tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id,plan_id,reason,actor_email) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6)`, m.AuditID, m.Event, m.AccountID, auditPlan, m.Reason, m.ActorEmail); err != nil {
		return out, unavailable(err)
	}
	out.WeeklyBytes, out.Sessions, out.NewStatus, out.NewRevision, out.NewGeneration, out.Security = weekly, sessions, newStatus, a.Revision+1, a.Generation, bool(m.Security)
	if m.Security {
		out.NewGeneration++
	}
	return out, nil
}

func (t *transaction) CreateQuotaPlan(ctx context.Context, p persistence.PlanCreate) (persistence.PlanCreateResult, error) {
	var out persistence.PlanCreateResult
	tag, err := t.tx.Exec(ctx, `INSERT INTO public.quota_plans(id,name,weekly_bytes,sessions,create_request_digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT (id) DO NOTHING`, p.ID, p.Name, p.WeeklyBytes, p.Sessions, p.Digest)
	if err != nil {
		return out, unavailable(err)
	}
	if tag.RowsAffected() == 0 {
		var stored []byte
		err = t.tx.QueryRow(ctx, `SELECT p.create_request_digest,EXISTS(SELECT 1 FROM public.audit_events e WHERE e.plan_id=p.id AND e.event='plan_created') FROM public.quota_plans p WHERE p.id=$1`, p.ID).Scan(&stored, &out.Audited)
		if err != nil {
			return out, unavailable(err)
		}
		out.Matching = len(stored) == len(p.Digest) && subtle.ConstantTimeCompare(stored, p.Digest) == 1
		return out, nil
	}
	if _, err = t.tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,plan_id,reason,actor_email) VALUES($1,clock_timestamp(),'plan_created',$2,$3,$4)`, p.AuditID, p.ID, p.Reason, p.ActorEmail); err != nil {
		return out, unavailable(err)
	}
	out.Created = true
	return out, nil
}

func (t *transaction) PlanCreateStatus(ctx context.Context, id string, digest []byte) (bool, error) {
	var stored []byte
	var audited bool
	err := t.tx.QueryRow(ctx, `SELECT p.create_request_digest,EXISTS(SELECT 1 FROM public.audit_events e WHERE e.plan_id=p.id AND e.event='plan_created') FROM public.quota_plans p WHERE p.id=$1`, id).Scan(&stored, &audited)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, unavailable(err)
	}
	return audited && len(stored) == len(digest) && subtle.ConstantTimeCompare(stored, digest) == 1, nil
}

func (t *transaction) ArchiveQuotaPlan(ctx context.Context, p persistence.PlanArchive) error {
	var revision int64
	var archived bool
	err := t.tx.QueryRow(ctx, `SELECT revision,archived_at IS NOT NULL FROM public.quota_plans WHERE id=$1 FOR UPDATE`, p.ID).Scan(&revision, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.ErrNotFound
	}
	if err != nil {
		return unavailable(err)
	}
	if revision != p.ExpectedRevision || archived {
		return persistence.ErrConflict
	}
	if _, err = t.tx.Exec(ctx, `UPDATE public.quota_plans SET archived_at=clock_timestamp(),revision=revision+1 WHERE id=$1`, p.ID); err != nil {
		return unavailable(err)
	}
	if _, err = t.tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,plan_id,reason,actor_email) VALUES($1,clock_timestamp(),'plan_archived',$2,$3,$4)`, p.AuditID, p.ID, p.Reason, p.ActorEmail); err != nil {
		return unavailable(err)
	}
	return nil
}

func (t *transaction) RevokeAccountAccess(ctx context.Context, id string, digest []byte, version uint64, auditID string) (persistence.AccountAccessState, error) {
	a, err := t.AccountAccessState(ctx, id, true)
	if err != nil {
		return a, err
	}
	var live bool
	err = t.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.portal_sessions ps JOIN public.accounts a ON a.id=ps.account_id WHERE ps.account_id=$1 AND ps.credential_digest=$2 AND ps.pepper_version=$3 AND ps.credential_generation=a.credential_generation AND ps.idle_expires_at>clock_timestamp() AND ps.absolute_expires_at>clock_timestamp())`, id, digest, version).Scan(&live)
	if err != nil {
		return a, unavailable(err)
	}
	if !live {
		return a, persistence.ErrInvalidCredential
	}
	if _, err = t.tx.Exec(ctx, `UPDATE public.accounts SET credential_generation=credential_generation+1 WHERE id=$1`, id); err == nil {
		_, err = t.tx.Exec(ctx, `UPDATE public.native_login_grants SET terminated_at=clock_timestamp() WHERE account_id=$1 AND terminated_at IS NULL`, id)
	}
	if err == nil {
		_, err = t.tx.Exec(ctx, `DELETE FROM public.portal_sessions WHERE account_id=$1`, id)
	}
	if err == nil {
		_, err = t.tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id) VALUES($1,clock_timestamp(),'owner_credentials_revoked',$2)`, auditID, id)
	}
	if err != nil {
		return a, unavailable(err)
	}
	return a, nil
}

var _ persistence.AccountAccessTransaction = (*transaction)(nil)
