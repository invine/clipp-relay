package auth_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/quota"
)

func TestOneServingBudgetCoversAccountQuotaAndCleanup(t *testing.T) {
	c, m, db := fixture(t)
	account := auth.NewWithRuntime(db, c, m, auth.Google())
	credit := quota.NewWithRuntime(db)
	defer credit.Close()
	id, err := quota.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(context.Background(), `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id) VALUES($1,'https://accounts.google.com',$2,'admission@example.test',true,clock_timestamp(),clock_timestamp(),clock_timestamp(),'Active','6dd09395-51a0-451c-96b3-716e6038e870')`, id, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.weekly_quota_usage WHERE account_id=$1; DELETE FROM public.accounts WHERE id=$1`, id)
	})

	entered := make(chan struct{}, 64)
	release := make(chan struct{})
	var workers sync.WaitGroup
	releaseGuards := sync.OnceFunc(func() { close(release) })
	defer func() {
		releaseGuards()
		workers.Wait()
	}()
	startGuard := func(n int) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			account.WithAccountGuards(context.Background(), []string{fmt.Sprintf("held-%d", n)}, func(context.Context) {
				entered <- struct{}{}
				<-release
			})
		}()
	}
	for n := range 63 {
		startGuard(n)
	}
	for range 63 {
		<-entered
	}
	var funded quota.Result
	var fundingErr error
	guarded := false
	admitted := account.WithServingUnit(context.Background(), func(unitCtx context.Context) {
		guarded = account.WithAccountGuards(unitCtx, []string{id}, func(guardCtx context.Context) {
			funded, fundingErr = credit.Ensure(guardCtx, id, 0)
		})
	})
	if !admitted || !guarded || fundingErr != nil || funded.Usable != quota.BlockBytes {
		t.Fatalf("nested serving-to-account-to-quota funding reacquired a unit: admitted=%t guarded=%t result=%+v err=%v", admitted, guarded, funded, fundingErr)
	}
	startGuard(63)
	<-entered
	start := time.Now()
	if account.WithAccountGuards(context.Background(), []string{"account"}, func(context.Context) {
		t.Error("account guard entered beyond budget")
	}) {
		t.Fatal("account guard admitted beyond budget")
	}
	if _, err := credit.Ensure(context.Background(), id, 0); !errors.Is(err, quota.ErrTemporary) {
		t.Fatalf("quota admitted beyond budget: %v", err)
	}
	if _, err := account.SampleCleanupSignals(context.Background()); err == nil {
		t.Fatal("cleanup read admitted beyond budget")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("overflow queued for %s", elapsed)
	}
	releaseGuards()
	workers.Wait()
	if _, err := account.SampleCleanupSignals(context.Background()); err != nil {
		t.Fatalf("cleanup did not recover after release: %v", err)
	}
	if !account.WithAccountGuards(context.Background(), []string{"account"}, func(context.Context) {}) {
		t.Fatal("account guard did not recover after release")
	}
}

func TestAccountGuardWaitObeysParentAndThreeSecondUnitDeadline(t *testing.T) {
	c, m, db := fixture(t)
	account := auth.NewWithRuntime(db, c, m, auth.Google())
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	releaseGuard := sync.OnceFunc(func() { close(release) })
	defer func() {
		releaseGuard()
		<-done
	}()
	go func() {
		defer close(done)
		account.WithAccountGuards(context.Background(), []string{"same-account"}, func(context.Context) {
			close(entered)
			<-release
		})
	}()
	<-entered
	parent, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	start := time.Now()
	if account.WithAccountGuards(parent, []string{"same-account"}, func(context.Context) {
		t.Error("entered a held guard after parent deadline")
	}) {
		t.Fatal("held guard ignored parent deadline")
	}
	cancel()
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("parent deadline was delayed: %s", elapsed)
	}
	start = time.Now()
	if account.WithAccountGuards(context.Background(), []string{"same-account"}, func(context.Context) {
		t.Error("entered a held guard after unit deadline")
	}) {
		t.Fatal("held guard ignored unit deadline")
	}
	if elapsed := time.Since(start); elapsed < 2800*time.Millisecond || elapsed > 3400*time.Millisecond {
		t.Fatalf("guard wait missed three-second unit deadline: %s", elapsed)
	}
	releaseGuard()
	<-done
}
