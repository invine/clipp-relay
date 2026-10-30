package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/quota"
)

func TestObservedDeletionFlushesAndImportsConfirmedUsage(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		expected                     int64
		fail, lost, late, activeFail bool
	}{
		{name: "confirmed-final-flush", expected: 12345},
		{name: "failed-final-flush", fail: true},
		{name: "failed-inflight-flush-cancelled-for-finalization", activeFail: true},
		{name: "committed-lost-acknowledgement", expected: 12345, lost: true},
		{name: "finalization-before-late-acknowledgement", expected: 12345, late: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := protectedPortalFixture(t)
			client := protectedClient(t, p)
			requireProtectedAuthentication(t, client, p, `"ok":true`)
			zero := strings.Repeat("0", 64)
			j := &deletionJournalFixture{head: auth.JournalHead{RepositoryID: "local-qualified-fixture", CoverageHash: zero, Hash: zero, Format: 1}, etag: "initial", events: map[string][]byte{}}
			p.auth.SetDeletionJournal(j, "local-qualified-fixture", 0, zero)
			var subject string
			if err := p.runtime.Pool.QueryRow(context.Background(), `SELECT subject FROM public.accounts WHERE id=$1`, p.account).Scan(&subject); err != nil {
				t.Fatal(err)
			}
			if tc.fail {
				j.failBeforeHeadOnce = true
			}
			now := time.Now().UTC()
			week := now.AddDate(0, 0, -(int(now.Weekday())+6)%7)
			week = time.Date(week.Year(), week.Month(), week.Day(), 0, 0, 0, 0, time.UTC)
			seedRecordedWeek(t, p, week.AddDate(0, 0, -7), 98765)
			if tc.lost {
				p.auth.SetDeletionFinalizeCommitHookForTest(func(ctx context.Context, tx persistence.DeletionTransaction) error {
					if err := tx.Commit(ctx); err != nil {
						return err
					}
					return errors.New("lost finalization acknowledgement")
				})
			}
			committed, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseAck := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseAck)
			if tc.late {
				p.quota.SetSettlementHooksForTest(nil, func(context.Context) error { close(committed); <-release; return errors.New("late lost reply") })
			}
			if tc.fail || tc.activeFail {
				var enteredOnce sync.Once
				p.quota.SetSettlementHooksForTest(func(ctx context.Context, _ persistence.AccountingTransaction) error {
					enteredOnce.Do(func() { close(committed) })
					<-ctx.Done()
					return ctx.Err()
				}, nil)
			}
			if tc.lost {
				p.quota.SetSettlementHooksForTest(nil, func(ctx context.Context) error { <-ctx.Done(); return errors.New("lost committed reply") })
			}
			if err := p.quota.Observe(p.account, 12345); err != nil {
				t.Fatal(err)
			}
			out := portalRequest(p.auth, "POST", "/auth/delete", p.owner, url.Values{"csrf": {csrfFrom(t, p.auth, p.owner)}}, p.config.PortalOrigin)
			if out.Code != http.StatusAccepted {
				t.Fatalf("delete %d %s", out.Code, out.Body.String())
			}
			deadline := time.Now().Add(2 * time.Second)
			for len(client.Network().ConnsToPeer(p.relay.Host.ID())) != 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if p.relay.AccountSessions(p.account) != 0 || len(client.Network().ConnsToPeer(p.relay.Host.ID())) != 0 {
				t.Fatal("deletion retained physical authority")
			}
			var flushDone chan error
			if tc.late || tc.activeFail {
				flushDone = make(chan error, 1)
				go func() { flushDone <- p.quota.FlushDeletionAccounting(context.Background(), p.account) }()
				select {
				case <-committed:
				case <-time.After(5 * time.Second):
					t.Fatal("settlement did not commit")
				}
			}
			if tc.fail {
				if err := p.auth.ReconcileOneDeletion(context.Background()); err == nil {
					t.Fatal("failed journal proof allowed finalization")
				}
				if got := portalRequest(p.auth, "GET", out.Header().Get("Location"), p.owner, nil, ""); got.Code != http.StatusAccepted {
					t.Fatal("journal failure did not retain pending deletion")
				}
				if _, err := p.runtime.Pool.Exec(context.Background(), `UPDATE public.deletion_operations SET next_attempt_at=clock_timestamp() WHERE account_id=$1`, p.account); err != nil {
					t.Fatal(err)
				}
			}
			started := time.Now()
			if err := p.auth.ReconcileOneDeletion(context.Background()); err != nil {
				t.Fatal(err)
			}
			if time.Since(started) > 3*time.Second {
				t.Fatal("accounting blocked deletion beyond bounded flush")
			}
			if tc.late || tc.activeFail {
				releaseAck()
				want := quota.ErrSettlementUncertain
				if tc.activeFail {
					want = quota.ErrTemporary
				}
				if err := <-flushDone; err != want {
					t.Fatalf("late acknowledgement %v", err)
				}
			}
			p.quota.SetSettlementHooksForTest(nil, nil)
			fresh := loginAs(t, p.auth, p.provider, subject, "fresh@example.test", "")
			if got := portalRequest(p.auth, "GET", "/", fresh, nil, ""); got.Code != 200 || !containsPending(got.Body.String()) {
				t.Fatal("registration inherited approval")
			}
			var id string
			if err := p.runtime.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, subject).Scan(&id); err != nil {
				t.Fatal(err)
			}

			usage, err := p.quota.RecordedUsage(context.Background(), id, week)
			if err != nil || usage.RecordedBytes != tc.expected || usage.Sequence != 0 || usage.OperationID != "" {
				t.Fatalf("opening usage %+v %v", usage, err)
			}
			_ = loginAs(t, p.auth, p.provider, subject, "again@example.test", "")
			repeated, e := p.quota.RecordedUsage(context.Background(), id, week)
			if e != nil || repeated.RecordedBytes != tc.expected || repeated.Sequence != 0 {
				t.Fatalf("repeated import %+v %v", repeated, e)
			}
			if err := p.quota.Observe(p.account, 1); err == nil {
				t.Fatal("deleted accounting state retained")
			}
		})
	}
}
