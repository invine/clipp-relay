package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"clipp-relay/internal/database"
	"github.com/jackc/pgx/v5"
)

// RecoveryRequest identifies one isolated restore. Target must be the actual
// database name; Source is the immutable backup identity, distinct from Target.
// Call only offline, after fencing all database and independent journal writers.
type RecoveryRequest struct{ RunID, Source, Target string }

// SetRecoveryCommitHookForTest controls only commit acknowledgment at the
// PostgreSQL boundary. Offline production recovery leaves this unset.
func (s *Server) SetRecoveryCommitHookForTest(commit func(context.Context, pgx.Tx) error) {
	s.commitRecovery = commit
}

// RecoverOffline proves the current independent journal before changing a
// restored database. It never creates journal events or reconstructs traffic.
// The completion receipt is atomic with all safety changes, so a lost reply can
// be retried without invalidating credentials issued after explicit review.
func (s *Server) RecoverOffline(ctx context.Context, request RecoveryRequest) error {
	if !idPattern.MatchString(request.RunID) || strings.TrimSpace(request.Source) == "" || len(request.Source) > 255 || request.Source == request.Target {
		return errors.New("invalid recovery identity")
	}
	if err := database.VerifySchema(ctx, s.db.Pool); err != nil {
		return err
	}
	var target string
	if err := s.db.QueryRow(ctx, `SELECT current_database()`).Scan(&target); err != nil {
		return err
	}
	if target != request.Target {
		return errors.New("recovery target mismatch")
	}
	completed, err := s.recoveryCompleted(ctx, request)
	if err != nil || completed {
		return err
	}
	head, etag, err := s.readHead(ctx)
	if err != nil {
		return err
	}
	accounts, err := s.committedDeletionAccounts(ctx, head, 0, "", true)
	if err != nil {
		return err
	}
	current, currentTag, err := s.readHead(ctx)
	if err != nil || current != head || currentTag != etag {
		return errors.New("recovery journal snapshot changed")
	}
	// All journal I/O completes before entering SQL locks. Offline writer fencing
	// is an operator prerequisite; no local transaction can fence an OCI writer.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(491847602137)`); err != nil {
		return err
	}
	var source, storedTarget string
	err = tx.QueryRow(ctx, `SELECT source,target FROM public.recovery_runs WHERE run_id=$1`, request.RunID).Scan(&source, &storedTarget)
	if err == nil {
		if source != request.Source || storedTarget != request.Target {
			return errors.New("recovery run identity conflict")
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM public.accounts ORDER BY id FOR UPDATE`)
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Issued credentials and Portal Sessions are durable and must be invalidated
	// on restore. Unfinished Google/consent continuations are RAM-only and vanish
	// with the process, so there is no flow table to clear here.
	for _, table := range []string{"authorization_codes", "relay_access_tokens", "refresh_generations", "login_grants", "portal_sessions"} {
		if _, err = tx.Exec(ctx, `DELETE FROM public.`+table); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE public.accounts SET credential_generation=credential_generation+1,recovery_review_hold=true,revision=revision+1 WHERE deleted_at IS NULL`); err != nil {
		return err
	}
	versions, err := tx.Query(ctx, `SELECT DISTINCT pepper_version FROM public.retained_quota_usage WHERE week_start>=date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`)
	if err != nil {
		return err
	}
	for versions.Next() {
		var version uint64
		if versions.Scan(&version) != nil || len(s.Peppers[version]) == 0 {
			versions.Close()
			return ErrRetainedPepperUnavailable
		}
	}
	err = versions.Err()
	versions.Close()
	if err != nil {
		return err
	}
	for _, id := range accounts {
		if err = s.recoverDeletedAccount(ctx, tx, id); err != nil {
			return err
		}
	}
	// No quota rows or compact receipts for survivors are rewritten. Retained
	// current-week imports remain the ordinary registration transaction's job.
	encoded, err := json.Marshal(head)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.recovery_runs(run_id,source,target,schema_revision,journal_head) VALUES($1,$2,$3,$4,$5)`, request.RunID, request.Source, request.Target, database.ExpectedRevision, encoded); err != nil {
		return err
	}
	if s.commitRecovery != nil {
		err = s.commitRecovery(ctx, tx)
	} else {
		err = tx.Commit(ctx)
	}
	if err == nil {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if done, checkErr := s.recoveryCompleted(checkCtx, request); checkErr == nil && done {
		return nil
	}
	return fmt.Errorf("recovery completion uncertain: %w", err)
}

func (s *Server) recoveryCompleted(ctx context.Context, r RecoveryRequest) (bool, error) {
	var source, target string
	var revision int
	err := s.db.QueryRow(ctx, `SELECT source,target,schema_revision FROM public.recovery_runs WHERE run_id=$1`, r.RunID).Scan(&source, &target, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if source != r.Source || target != r.Target || revision != database.ExpectedRevision {
		return false, errors.New("recovery run identity conflict")
	}
	return true, nil
}

func (s *Server) recoverDeletedAccount(ctx context.Context, tx pgx.Tx, id string) error {
	var subject *string
	var deleted *time.Time
	err := tx.QueryRow(ctx, `SELECT subject,deleted_at FROM public.accounts WHERE id=$1`, id).Scan(&subject, &deleted)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && deleted != nil {
		return nil
	}
	if err != nil {
		return err
	}
	if subject == nil {
		return errors.New("restored deletion identity missing")
	}
	if len(s.Peppers[s.CurrentPepper]) == 0 {
		return errors.New("retained usage key unavailable")
	}
	// This is the same current-time, confirmed-only seal as normal deletion.
	// GREATEST handles a restored retained record of this same logical deletion
	// without doubling it or reducing a more recent confirmed retained amount.
	query := `SELECT recorded_bytes FROM public.weekly_relay_usage WHERE account_id=$1 AND week_start=$2`
	var week time.Time
	if err = tx.QueryRow(ctx, `SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&week); err != nil {
		return err
	}
	var recorded int64
	err = tx.QueryRow(ctx, query, id, week).Scan(&recorded)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		version := s.CurrentPepper
		digest := s.digest(version, "retained-quota", googleIssuer+"\x00"+*subject)
		matches := 0
		for v := range s.Peppers {
			candidate := s.digest(v, "retained-quota", googleIssuer+"\x00"+*subject)
			var amount int64
			found := tx.QueryRow(ctx, `SELECT recorded_bytes FROM public.retained_quota_usage WHERE pepper_version=$1 AND identity_digest=$2 AND week_start=$3 FOR UPDATE`, v, candidate, week).Scan(&amount)
			if errors.Is(found, pgx.ErrNoRows) {
				continue
			}
			if found != nil {
				return found
			}
			matches++
			version, digest = v, candidate
		}
		if matches > 1 {
			return errors.New("duplicate logical retained usage")
		}
		if _, err = tx.Exec(ctx, `INSERT INTO public.retained_quota_usage(pepper_version,identity_digest,week_start,recorded_bytes) VALUES($1,$2,$3,$4) ON CONFLICT(pepper_version,identity_digest,week_start) DO UPDATE SET recorded_bytes=GREATEST(retained_quota_usage.recorded_bytes,EXCLUDED.recorded_bytes)`, version, digest, week, recorded); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE public.accounts SET issuer=NULL,subject=NULL,email=NULL,email_verified=NULL,hosted_domain=NULL,plan_id=NULL,weekly_bytes_override=NULL,sessions_override=NULL,status='Pending',deleted_at=clock_timestamp(),deletion_started_at=COALESCE(deletion_started_at,clock_timestamp()) WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE public.deletion_operations SET completed_at=clock_timestamp() WHERE account_id=$1 AND completed_at IS NULL`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE public.deletion_capacity SET pending=(SELECT count(*) FROM public.deletion_operations WHERE completed_at IS NULL) WHERE singleton`); err != nil {
		return err
	}
	audit, err := uuid()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id) VALUES($1,clock_timestamp(),'account_deleted',$2)`, audit, id)
	return err
}
