package auth_test

import (
	"context"
	"testing"
	"time"

	"clipp-relay/internal/auth"
)

func TestPepperRetirementRequiresCredentialCoverage(t *testing.T) {
	c, m, db := fixture(t)
	s := auth.New(db.Pool, c, m, newProvider(t).endpoints())
	ctx := context.Background()
	_, err := db.Pool.Exec(ctx, `INSERT INTO public.authorization_transactions(id,state_digest,nonce_digest,binding_digest,pepper_version,created_at,expires_at) VALUES('868d6764-6d30-4c82-8bb9-93e039dc1101',decode(repeat('ab',32),'hex'),decode(repeat('cd',32),'hex'),decode(repeat('ef',32),'hex'),992,clock_timestamp(),clock_timestamp()+interval '5 minutes')`)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Exec(ctx, `DELETE FROM public.authorization_transactions WHERE id='868d6764-6d30-4c82-8bb9-93e039dc1101'`)
	if err := s.ValidatePepperCoverage(ctx); err == nil {
		t.Fatal("premature pepper retirement accepted")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE public.authorization_transactions SET expires_at=clock_timestamp()-interval '23 hours' WHERE id='868d6764-6d30-4c82-8bb9-93e039dc1101'`); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidatePepperCoverage(ctx); err == nil {
		t.Fatal("pepper removed inside cleanup window")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE public.authorization_transactions SET expires_at=clock_timestamp()-interval '25 hours' WHERE id='868d6764-6d30-4c82-8bb9-93e039dc1101'`); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidatePepperCoverage(ctx); err != nil {
		t.Fatalf("expired dependent still blocks retirement: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE public.authorization_transactions SET expires_at=clock_timestamp()+interval '5 minutes' WHERE id='868d6764-6d30-4c82-8bb9-93e039dc1101'`); err != nil {
		t.Fatal(err)
	}
	s.Peppers[992] = make([]byte, 32)
	if err := s.ValidatePepperCoverage(ctx); err != nil {
		t.Fatalf("overlapping keyring rejected: %v", err)
	}
}

func TestCleanupSignalsReportRetentionBreach(t *testing.T) {
	c, m, db := fixture(t)
	s := auth.New(db.Pool, c, m, newProvider(t).endpoints())
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
	restarted := auth.New(db.Pool, c, m, newProvider(t).endpoints())
	persisted, err := restarted.SampleCleanupSignals(ctx)
	if err != nil || persisted.BreachEpisodes <= baseline {
		t.Fatalf("breach episode lost across restart: %+v %v", persisted, err)
	}
}
