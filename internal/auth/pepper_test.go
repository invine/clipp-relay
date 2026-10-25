package auth_test

import (
	"context"
	"testing"
	"time"

	"clipp-relay/internal/diagnostic"
)

func TestPepperRetirementRequiresCredentialCoverage(t *testing.T) {
	c, m, db := fixture(t)
	s := newTestServer(t, db.Pool, c, m, newProvider(t).endpoints())
	ctx := context.Background()
	_, err := db.Pool.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at)
VALUES('868d6764-6d30-4c82-8bb9-93e039dc1101','https://accounts.google.com','pepper-session-coverage','pepper@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp());
INSERT INTO public.portal_sessions(credential_digest,pepper_version,account_id,credential_generation,csrf_digest,google_authenticated_at,created_at,last_used_at,idle_expires_at,absolute_expires_at)
VALUES(decode(repeat('ab',32),'hex'),992,'868d6764-6d30-4c82-8bb9-93e039dc1101',0,decode(repeat('cd',32),'hex'),clock_timestamp(),clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '5 minutes',clock_timestamp()+interval '1 hour')`)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.portal_sessions WHERE account_id='868d6764-6d30-4c82-8bb9-93e039dc1101'; DELETE FROM public.accounts WHERE id='868d6764-6d30-4c82-8bb9-93e039dc1101'`)
	if err := s.ValidatePepperCoverage(ctx); err == nil {
		t.Fatal("premature pepper retirement accepted")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE public.portal_sessions SET idle_expires_at=clock_timestamp()-interval '23 hours' WHERE account_id='868d6764-6d30-4c82-8bb9-93e039dc1101'`); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidatePepperCoverage(ctx); err == nil {
		t.Fatal("pepper removed inside cleanup window")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE public.portal_sessions SET idle_expires_at=clock_timestamp()-interval '25 hours' WHERE account_id='868d6764-6d30-4c82-8bb9-93e039dc1101'`); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidatePepperCoverage(ctx); err != nil {
		t.Fatalf("expired dependent still blocks retirement: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE public.portal_sessions SET idle_expires_at=clock_timestamp()+interval '5 minutes' WHERE account_id='868d6764-6d30-4c82-8bb9-93e039dc1101'`); err != nil {
		t.Fatal(err)
	}
	s.Peppers[992] = make([]byte, 32)
	if err := s.ValidatePepperCoverage(ctx); err != nil {
		t.Fatalf("overlapping keyring rejected: %v", err)
	}
}

func TestCleanupSignalsReportRetentionBreach(t *testing.T) {
	c, m, db := fixture(t)
	s := newTestServer(t, db.Pool, c, m, newProvider(t).endpoints())
	ctx := context.Background()
	_, err := db.Pool.Exec(ctx, `INSERT INTO public.audit_events(id,occurred_at,event,plan_id,reason,actor_email) VALUES('66f8011d-64fb-4988-9211-933188310914',clock_timestamp()-interval '182 days','plan_created','6dd09395-51a0-451c-96b3-716e6038e870','routine_administration','operator@example.test')`)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.audit_events WHERE id='66f8011d-64fb-4988-9211-933188310914'`)
	signals, err := s.SampleCleanupSignals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if signals.OldestAge < 47*time.Hour || !signals.Warning || !signals.Critical || !signals.Breach {
		t.Fatalf("retention breach invisible: %+v", signals)
	}
	var events []diagnostic.Event
	observations := diagnostic.NewObservations(func(e diagnostic.Event) { events = append(events, e) }, func(ctx context.Context) (string, error) {
		v, err := s.SampleCleanupSignals(ctx)
		if err != nil {
			return "", err
		}
		switch {
		case v.Breach:
			return "breach", nil
		case v.Critical:
			return "critical", nil
		case v.Warning:
			return "warning", nil
		default:
			return "none", nil
		}
	}, nil)
	observations.Tick(ctx, time.Now())
	observations.Wait()
	breachLogged := false
	for _, e := range events {
		if e.Event == "observation.threshold" && e.Signal == "cleanup" && e.Reason == "breach" {
			breachLogged = true
		}
	}
	if !breachLogged {
		t.Fatalf("real SQL breach not logged: %+v", events)
	}
	baseline := signals.BreachEpisodes
	if _, err := db.Pool.Exec(ctx, `UPDATE public.cleanup_breach_record SET active=false WHERE singleton=true`); err != nil {
		t.Fatal(err)
	}
	maintainCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.Maintain(maintainCtx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var retained int
		var episodes uint64
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE id='66f8011d-64fb-4988-9211-933188310914'`).Scan(&retained); err != nil {
			t.Fatal(err)
		}
		if err := db.Pool.QueryRow(ctx, `SELECT episodes FROM public.cleanup_breach_record WHERE singleton=true`).Scan(&episodes); err != nil {
			t.Fatal(err)
		}
		if retained == 0 && episodes > baseline {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	restarted := newTestServer(t, db.Pool, c, m, newProvider(t).endpoints())
	persisted, err := restarted.SampleCleanupSignals(ctx)
	if err != nil || persisted.BreachEpisodes <= baseline {
		t.Fatalf("breach episode lost across restart: %+v %v", persisted, err)
	}
	observations.Tick(ctx, time.Now().Add(time.Minute))
	observations.Wait()
	if persisted.Breach {
		t.Fatalf("cleanup did not clear real SQL breach: %+v", persisted)
	}
	cleared := false
	for _, e := range events {
		if e.Event == "observation.threshold" && e.Signal == "cleanup" && e.Reason != "breach" {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("cleanup threshold clearance not logged: %+v", events)
	}
}
