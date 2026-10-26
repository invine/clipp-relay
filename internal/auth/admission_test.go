package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/quota"
)

func TestQuotaEligibilityUsesItsOwnDatabaseBudget(t *testing.T) {
	c, m, db := fixture(t)
	account := auth.NewWithRuntime(db, c, m, auth.Google())
	accounting := quota.NewWithRuntime(db)
	defer accounting.Close()
	id, err := quota.NewOperationID()
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

	entered := make(chan struct{}, 64)
	release := make(chan struct{})
	releaseUnits := sync.OnceFunc(func() { close(release) })
	var workers sync.WaitGroup
	defer func() { releaseUnits(); workers.Wait() }()
	startUnit := func() {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_ = db.WithUnit(context.Background(), func(context.Context) error {
				entered <- struct{}{}
				<-release
				return nil
			})
		}()
	}
	for range 63 {
		startUnit()
	}
	for range 63 {
		<-entered
	}
	if result, err := accounting.Eligibility(context.Background(), id); err != nil || result.EffectiveBytes != 0 {
		t.Fatalf("quota did not use remaining database capacity: result=%+v err=%v", result, err)
	}
	startUnit()
	<-entered
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
	releaseUnits()
	workers.Wait()
	if _, err := accounting.Eligibility(context.Background(), id); err != nil {
		t.Fatalf("quota did not recover after release: %v", err)
	}
	if _, err := account.SampleCleanupSignals(context.Background()); err != nil {
		t.Fatalf("cleanup did not recover after release: %v", err)
	}
}
