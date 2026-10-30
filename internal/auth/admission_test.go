package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/persistence/postgres"
	"clipp-relay/internal/quota"
)

func TestQuotaEligibilityUsesItsOwnDatabaseBudget(t *testing.T) {
	c, m, db := fixture(t)
	account := newAuthWithRuntime(db, c, m, auth.Google())
	accounting := quota.New(postgres.New(db))
	defer accounting.Close()
	id, err := testOperationID()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(context.Background(), `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id) VALUES($1,'https://accounts.google.com',$2,'quota-budget@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp(),'Active','6dd09395-51a0-451c-96b3-716e6038e870')`, id, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.weekly_relay_usage WHERE account_id=$1; DELETE FROM public.accounts WHERE id=$1`, id)
	})

	work := postgres.New(db)
	ordinary := make([]persistence.Operation, 0, 63)
	defer func() {
		for _, op := range ordinary {
			op.Close()
		}
	}()
	startOrdinary := func() {
		t.Helper()
		op, err := work.Start(context.Background())
		if err != nil {
			t.Fatalf("ordinary operation did not reserve admission: %v", err)
		}
		ordinary = append(ordinary, op)
	}
	for range 63 {
		startOrdinary()
	}
	settlement, err := postgres.New(db).StartSettlement(context.Background())
	if err != nil {
		t.Fatalf("settlement did not reserve the last admission slot: %v", err)
	}
	defer settlement.Close()
	if _, err := work.Start(context.Background()); !errors.Is(err, persistence.ErrWorkUnavailable) || !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf("ordinary operation did not reject at the shared capacity limit: %v", err)
	}
	if _, err := postgres.New(db).StartSettlement(context.Background()); !errors.Is(err, persistence.ErrUnavailable) {
		t.Fatalf("settlement did not reject at the shared capacity limit: %v", err)
	}
	start := time.Now()
	if _, err := accounting.Eligibility(context.Background(), id); !errors.Is(err, quota.ErrTemporary) {
		t.Fatalf("quota admitted beyond database budget: %v", err)
	}
	if _, err := account.SampleCleanupSignals(context.Background()); err == nil {
		t.Fatal("cleanup read admitted beyond database budget")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("overflow queued for %s", elapsed)
	}
	settlement.Close()
	settlement, err = postgres.New(db).StartSettlement(context.Background())
	if err != nil {
		t.Fatalf("settlement did not recover after its prior scope closed: %v", err)
	}
	settlement.Close()
	for _, op := range ordinary {
		op.Close()
	}
	startOrdinary()
	ordinary[len(ordinary)-1].Close()
	if _, err := accounting.Eligibility(context.Background(), id); err != nil {
		t.Fatalf("quota did not recover after release: %v", err)
	}
	if _, err := account.SampleCleanupSignals(context.Background()); err != nil {
		t.Fatalf("cleanup did not recover after release: %v", err)
	}
}
