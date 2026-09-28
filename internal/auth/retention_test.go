package auth_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
)

func retainedDigest(key []byte, subject string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte("clipp-relay/v1/retained-quota\x00https://accounts.google.com\x00" + subject))
	return h.Sum(nil)
}

func TestMaintenanceKeepsLiveGrantAndRetentionBoundaries(t *testing.T) {
	c, m, db := fixture(t)
	s := auth.New(db.Pool, c, m, newProvider(t).endpoints())
	ctx := context.Background()
	id := "5f3d87a7-8f7b-478f-8a4b-8adcfde941d7"
	grant := "bfca2164-f32b-40a9-bb1f-cd0d51c682a9"
	_, err := db.Pool.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id)
VALUES($1,'https://accounts.google.com','retention-boundary','boundary@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp()-interval '200 days','Active','6dd09395-51a0-451c-96b3-716e6038e870')`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.accounts WHERE id=$1`, id)
	for _, offset := range []int{0, 12, 13} {
		_, err = db.Pool.Exec(ctx, `INSERT INTO public.weekly_quota_usage(account_id,week_start,committed_bytes,sequence,latest_operation_id,latest_granted_bytes)
VALUES($1,date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-7*$2,10,1,'ad243d69-f4a1-4556-bd90-72c8b2b3b09f',10)`, id, offset)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.weekly_quota_usage WHERE account_id=$1`, id)
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.login_grants(id,account_id,credential_generation,client_type,current_refresh_generation,created_at,last_used_at,idle_expires_at,absolute_expires_at)
VALUES($1,$2,0,'electron',2,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '7 days',clock_timestamp()+interval '30 days')`, grant, id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.login_grants WHERE id=$1`, grant)
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.refresh_generations(credential_digest,pepper_version,grant_id,generation,issued_at,consumed_at)
VALUES(decode(repeat('ab',32),'hex'),$1,$2,1,clock_timestamp()-interval '2 days',clock_timestamp()-interval '2 days')`, s.CurrentPepper, grant)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.refresh_generations WHERE grant_id=$1`, grant)
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.retained_quota_usage(pepper_version,identity_digest,week_start,committed_bytes)
VALUES($1,decode(repeat('cd',32),'hex'),date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-7,10),
($1,decode(repeat('ef',32),'hex'),date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date,10)`, s.CurrentPepper)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.retained_quota_usage WHERE identity_digest IN (decode(repeat('cd',32),'hex'),decode(repeat('ef',32),'hex'))`)
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,plan_id,reason,actor_email)
VALUES('2d41eb9e-c19a-4dcf-9094-f64123115573',clock_timestamp()-interval '181 days','plan_created','6dd09395-51a0-451c-96b3-716e6038e870','routine_administration','operator@example.test'),
('24775912-acbe-4226-bbf9-c8da7d17877d',clock_timestamp()-interval '179 days','plan_created','6dd09395-51a0-451c-96b3-716e6038e870','routine_administration','operator@example.test')`)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.audit_events WHERE id IN ('2d41eb9e-c19a-4dcf-9094-f64123115573','24775912-acbe-4226-bbf9-c8da7d17877d')`)
	maintainCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Maintain(maintainCtx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var oldHistory, oldRetained, oldAudit int
		_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.weekly_quota_usage WHERE account_id=$1 AND week_start=date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-91`, id).Scan(&oldHistory)
		_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.retained_quota_usage WHERE identity_digest=decode(repeat('cd',32),'hex')`).Scan(&oldRetained)
		_ = db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE id='2d41eb9e-c19a-4dcf-9094-f64123115573'`).Scan(&oldAudit)
		if oldHistory == 0 && oldRetained == 0 && oldAudit == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var history, retained, expiredRetained, liveHash, account, oldAudit, currentAudit int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.weekly_quota_usage WHERE account_id=$1`, id).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.retained_quota_usage WHERE identity_digest=decode(repeat('ef',32),'hex')`).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.retained_quota_usage WHERE identity_digest=decode(repeat('cd',32),'hex')`).Scan(&expiredRetained); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.refresh_generations WHERE grant_id=$1`, grant).Scan(&liveHash); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.accounts WHERE id=$1`, id).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE id='2d41eb9e-c19a-4dcf-9094-f64123115573'`).Scan(&oldAudit); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE id='24775912-acbe-4226-bbf9-c8da7d17877d'`).Scan(&currentAudit); err != nil {
		t.Fatal(err)
	}
	if history != 2 || retained != 1 || expiredRetained != 0 || liveHash != 1 || account != 1 || oldAudit != 0 || currentAudit != 1 {
		t.Fatalf("retention boundary: history=%d retained=%d expired retained=%d live hash=%d active account=%d old audit=%d current audit=%d", history, retained, expiredRetained, liveHash, account, oldAudit, currentAudit)
	}
}

func TestCompletedDeletionPurgesHiddenAccountButKeepsDetachedAudit(t *testing.T) {
	c, m, db := fixture(t)
	s := auth.New(db.Pool, c, m, newProvider(t).endpoints())
	ctx := context.Background()
	id := "3184eac8-27c9-4308-9a32-031103e84a4a"
	op := "458c6c6d-a0d2-4d93-b092-f969e17c8cd0"
	audit := "108f3c66-0837-4529-b6f4-c4378cb6b2d5"
	_, err := db.Pool.Exec(ctx, `INSERT INTO public.accounts(id,validated_at,created_at,last_portal_login_at,status,deletion_started_at,deleted_at)
VALUES($1,clock_timestamp()-interval '2 hours',clock_timestamp()-interval '2 hours',clock_timestamp()-interval '2 hours','Pending',clock_timestamp()-interval '2 hours',clock_timestamp()-interval '2 hours')`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.accounts WHERE id=$1`, id)
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.deletion_operations(id,account_id,admitted_at,next_attempt_at,completed_at)
VALUES($1,$2,clock_timestamp()-interval '2 hours',clock_timestamp()-interval '2 hours',clock_timestamp()-interval '2 hours')`, op, id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.deletion_operations WHERE id=$1`, op)
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,account_id)
VALUES($1,clock_timestamp()-interval '2 hours','account_deleted',$2)`, audit, id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.audit_events WHERE id=$1`, audit)
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.weekly_quota_usage(account_id,week_start,committed_bytes,sequence,latest_operation_id,latest_granted_bytes)
VALUES($1,date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date,8,1,'506d5f55-fd80-48a5-bd4e-c99db74b8389',8)`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.weekly_quota_usage WHERE account_id=$1`, id)
	maintainCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Maintain(maintainCtx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.accounts WHERE id=$1`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var account, operation, quota, auditCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.accounts WHERE id=$1`, id).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.deletion_operations WHERE id=$1`, op).Scan(&operation); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.weekly_quota_usage WHERE account_id=$1`, id).Scan(&quota); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE id=$1 AND account_id=$2`, audit, id).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if account != 0 || operation != 0 || quota != 0 || auditCount != 1 {
		t.Fatalf("completed purge: account=%d operation=%d quota=%d detached audit=%d", account, operation, quota, auditCount)
	}
}

func TestRetainedUsagePreventsPrematurePepperRetirement(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "pepper-missing-registration"
	s := auth.New(db.Pool, c, m, d.endpoints())
	ctx := context.Background()
	_, err := db.Pool.Exec(ctx, `INSERT INTO public.retained_quota_usage(pepper_version,identity_digest,week_start,committed_bytes)
VALUES(993,decode(repeat('fa',32),'hex'),date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date,100)`)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.retained_quota_usage WHERE pepper_version=993`)
	if err := s.ValidatePepperCoverage(ctx); err == nil {
		t.Fatal("current-week retained usage key removed")
	}
	flow, binding := start(t, s, nil)
	if w := complete(s, flow, binding); w.Code != 503 {
		t.Fatalf("registration with missing retained key: %d", w.Code)
	}
	var created int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.accounts WHERE subject=$1`, d.subject).Scan(&created); err != nil || created != 0 {
		t.Fatalf("failed registration persisted identity: count=%d err=%v", created, err)
	}
	s.Peppers[993] = make([]byte, 32)
	if err := s.ValidatePepperCoverage(ctx); err != nil {
		t.Fatalf("overlapping key rejected: %v", err)
	}
	flow, binding = start(t, s, nil)
	if w := complete(s, flow, binding); w.Code != 303 {
		t.Fatalf("registration after key restore: %d", w.Code)
	}
	defer func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.portal_sessions WHERE account_id IN (SELECT id FROM public.accounts WHERE subject=$1)`, d.subject)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM public.accounts WHERE subject=$1`, d.subject)
	}()
}

func TestCleanupErrorDoesNotStarveLaterRetentionClass(t *testing.T) {
	c, m, db := fixture(t)
	s := auth.New(db.Pool, c, m, newProvider(t).endpoints())
	ctx := context.Background()
	_, err := db.Pool.Exec(ctx, `INSERT INTO public.retained_quota_usage(pepper_version,identity_digest,week_start,committed_bytes)
VALUES($1,decode(repeat('da',32),'hex'),date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-7,1)`, s.CurrentPepper)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.retained_quota_usage WHERE identity_digest=decode(repeat('da',32),'hex')`)
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,plan_id,reason,actor_email)
VALUES('80975492-9579-4df7-8aa6-1ce9757be7ca',clock_timestamp()-interval '181 days','plan_created','6dd09395-51a0-451c-96b3-716e6038e870','routine_administration','operator@example.test')`)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.audit_events WHERE id='80975492-9579-4df7-8aa6-1ce9757be7ca'`)
	lock, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err := lock.Exec(ctx, `SELECT 1 FROM public.retained_quota_usage WHERE identity_digest=decode(repeat('da',32),'hex') FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	maintainCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Maintain(maintainCtx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var oldAudit int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE id='80975492-9579-4df7-8aa6-1ce9757be7ca'`).Scan(&oldAudit); err != nil {
			t.Fatal(err)
		}
		if oldAudit == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var oldAudit, lockedRetained int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE id='80975492-9579-4df7-8aa6-1ce9757be7ca'`).Scan(&oldAudit); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.retained_quota_usage WHERE identity_digest=decode(repeat('da',32),'hex')`).Scan(&lockedRetained); err != nil {
		t.Fatal(err)
	}
	if oldAudit != 0 || lockedRetained != 1 {
		t.Fatalf("locked class starved audit or bypassed lock: audit=%d retained=%d", oldAudit, lockedRetained)
	}
	cancel()
	<-done
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	restarted := auth.New(db.Pool, c, m, newProvider(t).endpoints())
	restartCtx, stopRestart := context.WithCancel(ctx)
	restartDone := make(chan struct{})
	go func() { restarted.Maintain(restartCtx); close(restartDone) }()
	defer func() { stopRestart(); <-restartDone }()
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.retained_quota_usage WHERE identity_digest=decode(repeat('da',32),'hex')`).Scan(&lockedRetained); err != nil {
			t.Fatal(err)
		}
		if lockedRetained == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if lockedRetained != 0 {
		t.Fatal("restart did not catch up locked retention class")
	}
}

func TestDuplicateLogicalRetainedUsageFailsRegistration(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "duplicate-retained-usage"
	s := auth.New(db.Pool, c, m, d.endpoints())
	s.Peppers[993] = make([]byte, 32)
	ctx := context.Background()
	for _, version := range []uint64{s.CurrentPepper, 993} {
		_, err := db.Pool.Exec(ctx, `INSERT INTO public.retained_quota_usage(pepper_version,identity_digest,week_start,committed_bytes)
VALUES($1,$2,date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date,100)`, version, retainedDigest(s.Peppers[version], d.subject))
		if err != nil {
			t.Fatal(err)
		}
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.retained_quota_usage WHERE (pepper_version=$1 AND identity_digest=$2) OR (pepper_version=993 AND identity_digest=$3)`, s.CurrentPepper, retainedDigest(s.Peppers[s.CurrentPepper], d.subject), retainedDigest(s.Peppers[993], d.subject))
	flow, binding := start(t, s, nil)
	if got := complete(s, flow, binding); got.Code != 503 {
		t.Fatalf("duplicate logical record accepted: %d", got.Code)
	}
	var accounts, retained int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.accounts WHERE subject=$1`, d.subject).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.retained_quota_usage WHERE identity_digest=$1 OR (pepper_version=993 AND identity_digest=$2)`, retainedDigest(s.Peppers[s.CurrentPepper], d.subject), retainedDigest(s.Peppers[993], d.subject)).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if accounts != 0 || retained != 2 {
		t.Fatalf("duplicate state mutated: accounts=%d retained=%d", accounts, retained)
	}
}

func TestConcurrentRegistrationImportsRetainedUsageOnce(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "concurrent-retained-usage"
	s := auth.New(db.Pool, c, m, d.endpoints())
	ctx := context.Background()
	digest := retainedDigest(s.Peppers[s.CurrentPepper], d.subject)
	_, err := db.Pool.Exec(ctx, `INSERT INTO public.retained_quota_usage(pepper_version,identity_digest,week_start,committed_bytes)
VALUES($1,$2,date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date,123)`, s.CurrentPepper, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.retained_quota_usage WHERE identity_digest=$1`, digest)
	const flows = 4
	states := make([]string, flows)
	bindings := make([]*http.Cookie, flows)
	for i := range states {
		states[i], bindings[i] = start(t, s, nil)
	}
	var wg sync.WaitGroup
	results := make(chan int, flows)
	for i := range states {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results <- complete(s, states[i], bindings[i]).Code }(i)
	}
	wg.Wait()
	close(results)
	for code := range results {
		if code != 303 {
			t.Errorf("concurrent registration status %d", code)
		}
	}
	var accounts, remaining int
	var committed, sequence int64
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.accounts WHERE subject=$1`, d.subject).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT u.committed_bytes,u.sequence FROM public.weekly_quota_usage u JOIN public.accounts a ON a.id=u.account_id WHERE a.subject=$1 AND u.week_start=date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`, d.subject).Scan(&committed, &sequence); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.retained_quota_usage WHERE identity_digest=$1`, digest).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 || committed != 123 || sequence != 0 || remaining != 0 {
		t.Fatalf("concurrent import: accounts=%d committed=%d sequence=%d retained=%d", accounts, committed, sequence, remaining)
	}
}

func TestHiddenAccountBatchDoesNotMonopolizeOtherAccounts(t *testing.T) {
	c, m, db := fixture(t)
	s := auth.New(db.Pool, c, m, newProvider(t).endpoints())
	ctx := context.Background()
	oldID := "c9d3edce-e472-4a0d-b8ed-e45e20b753c1"
	newID := "042d4989-6695-4b1f-901b-2e9defb53903"
	_, err := db.Pool.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at)
VALUES($1,'https://accounts.google.com','fair-old','old@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp()-interval '92 days'),
($2,'https://accounts.google.com','fair-new','new@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp()-interval '91 days')`, oldID, newID)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.accounts WHERE id IN ($1,$2)`, oldID, newID)
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.portal_sessions(credential_digest,pepper_version,account_id,credential_generation,csrf_digest,google_authenticated_at,created_at,last_used_at,idle_expires_at,absolute_expires_at)
SELECT decode(lpad(to_hex(n),64,'0'),'hex'),$1,$2,0,decode(repeat('ab',32),'hex'),clock_timestamp(),clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '12 hours'
FROM generate_series(1,5000) n`, s.CurrentPepper, oldID)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.portal_sessions WHERE account_id IN ($1,$2)`, oldID, newID)
	_, err = db.Pool.Exec(ctx, `INSERT INTO public.portal_sessions(credential_digest,pepper_version,account_id,credential_generation,csrf_digest,google_authenticated_at,created_at,last_used_at,idle_expires_at,absolute_expires_at)
VALUES(decode(lpad(to_hex(5001),64,'0'),'hex'),$1,$2,0,decode(repeat('cd',32),'hex'),clock_timestamp(),clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '12 hours')`, s.CurrentPepper, newID)
	if err != nil {
		t.Fatal(err)
	}
	maintainCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Maintain(maintainCtx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	var newer, olderChildren int
	for time.Now().Before(deadline) {
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.accounts WHERE id=$1`, newID).Scan(&newer); err != nil {
			t.Fatal(err)
		}
		if newer == 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.portal_sessions WHERE account_id=$1`, oldID).Scan(&olderChildren); err != nil {
		t.Fatal(err)
	}
	if newer != 0 || olderChildren == 0 {
		t.Fatalf("large account monopolized cleanup: newer=%d older children=%d", newer, olderChildren)
	}
}
