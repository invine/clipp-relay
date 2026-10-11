package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/database"
	"clipp-relay/internal/diagnostic"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type diagnosticOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (o *diagnosticOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.Buffer.Write(p)
}

func (o *diagnosticOutput) rows() []map[string]any {
	o.mu.Lock()
	defer o.mu.Unlock()
	var rows []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(o.Bytes()), []byte("\n")) {
		var row map[string]any
		if json.Unmarshal(line, &row) == nil {
			rows = append(rows, row)
		}
	}
	return rows
}

func requireQuotaRow(t *testing.T, out *diagnosticOutput, event, outcome, reason string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, row := range out.rows() {
			if row["event"] == event && row["outcome"] == outcome && row["reason"] == reason {
				return row
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("missing %s/%s/%s: %v", event, outcome, reason, out.rows())
	return nil
}

func TestQuotaDiagnosticsKnownRollbackAndReconciledAllocation(t *testing.T) {
	q, id, _ := fixture(t)
	out := &diagnosticOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	q.SetDiagnostics(log)
	q.beforeCommit = func(context.Context, pgx.Tx) error {
		q.beforeCommit = nil
		return errors.New("private-rollback-canary")
	}
	if _, err := q.Ensure(context.Background(), id, 0); !errors.Is(err, ErrTemporary) {
		t.Fatalf("rollback: %v", err)
	}
	requireQuotaRow(t, out, "quota.funding", "failed", "database_unavailable")
	q.afterCommit = func() error {
		q.afterCommit = nil
		return errors.New("private-lost-ack-canary")
	}
	if _, err := q.Ensure(context.Background(), id, 0); !errors.Is(err, ErrTemporary) {
		t.Fatalf("ambiguous allocation: %v", err)
	}
	requireQuotaRow(t, out, "quota.funding", "uncertain", "allocation_unconfirmed")
	got, err := q.Ensure(context.Background(), id, 0)
	if err != nil || got.Committed != BlockBytes || got.Usable != BlockBytes {
		t.Fatalf("reconciliation: %+v %v", got, err)
	}
	requireQuotaRow(t, out, "quota.funding", "success", "none")
	for _, row := range out.rows() {
		if row["request_id"] != nil || row["session_id"] != nil {
			t.Fatalf("invented correlation: %v", row)
		}
	}
	encoded, _ := json.Marshal(out.rows())
	for _, canary := range []string{id, "private-rollback-canary", "private-lost-ack-canary"} {
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("private detail leaked: %s", canary)
		}
	}
}

func TestQuotaDiagnosticsRefusalRetirementAndBoundedOutput(t *testing.T) {
	q, id, pool := fixture(t)
	out := &diagnosticOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	q.SetDiagnostics(log)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE public.accounts SET weekly_bytes_override=0 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		if _, err := q.Ensure(ctx, id, 0); !errors.Is(err, ErrExhausted) {
			t.Fatalf("exhaustion %d: %v", i, err)
		}
	}
	requireQuotaRow(t, out, "quota.funding", "refused", "quota_exhausted")
	if _, err := pool.Exec(ctx, `UPDATE public.accounts SET weekly_bytes_override=$2,status='Suspended' WHERE id=$1`, id, 2*BlockBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Ensure(ctx, id, 0); !errors.Is(err, ErrTemporary) {
		t.Fatalf("blocked account: %v", err)
	}
	requireQuotaRow(t, out, "quota.funding", "refused", "account_blocked")
	if _, err := pool.Exec(ctx, `UPDATE public.accounts SET status='Active' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Ensure(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	requireQuotaRow(t, out, "quota.funding", "success", "none")
	if _, err := q.Take(ctx, id, 0, 10); err != nil {
		t.Fatal(err)
	}
	q.Invalidate(id)
	requireQuotaRow(t, out, "quota.credit_retired", "success", "discarded")
	rows := out.rows()
	exhausted := 0
	fundingSuccess := 0
	for _, row := range rows {
		if row["event"] == "quota.funding" && row["reason"] == "quota_exhausted" {
			exhausted++
		}
		if row["event"] == "quota.funding" && row["outcome"] == "success" {
			fundingSuccess++
		}
	}
	if exhausted > 5 || fundingSuccess != 1 {
		t.Fatalf("unbounded refusals or byte callbacks logged as funding: exhausted=%d success=%d", exhausted, fundingSuccess)
	}
	encoded, _ := json.Marshal(rows)
	if strings.Contains(string(encoded), id) || strings.Contains(string(encoded), "quota@example.test") {
		t.Fatal("account identity leaked")
	}
}

func TestQuotaDiagnosticsDatabaseOutageAndClockFailure(t *testing.T) {
	q, id, pool := fixture(t)
	out := &diagnosticOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	q.SetDiagnostics(log)
	ctx := context.Background()
	if _, err := q.Ensure(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	q.probeQuery = func(context.Context) (time.Time, error) {
		return time.Now().Add(6 * time.Second), nil
	}
	if err := q.Probe(ctx); !errors.Is(err, ErrTemporary) {
		t.Fatalf("clock probe: %v", err)
	}
	if _, err := q.Take(ctx, id, 0, 1); !errors.Is(err, ErrTemporary) {
		t.Fatalf("unsafe clock spent credit: %v", err)
	}
	requireQuotaRow(t, out, "quota.funding", "failed", "clock_unavailable")
	q.probeQuery = nil
	q.clock.mu.Lock()
	q.clock.sampled = time.Time{}
	q.clock.safe = false
	q.clock.mu.Unlock()
	if err := q.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Take(ctx, id, 0, BlockBytes); err != nil {
		t.Fatalf("confirmed credit before outage: %v", err)
	}
	offline, err := pgxpool.NewWithConfig(ctx, pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	q.runtime = database.NewRuntime(offline)
	offline.Close()
	if _, err := q.Take(ctx, id, 0, 1); !errors.Is(err, ErrTemporary) {
		t.Fatalf("database outage: %v", err)
	}
	requireQuotaRow(t, out, "quota.funding", "failed", "database_unavailable")
}

func TestQuotaDiagnosticsExpiredCreditIsRetiredWithoutRefund(t *testing.T) {
	q, id, pool := fixture(t)
	out := &diagnosticOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	q.SetDiagnostics(log)
	ctx := context.Background()
	if _, err := q.Ensure(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	boundary := weekStart(time.Now()).AddDate(0, 0, 7).Add(2 * time.Second)
	shift := boundary.Sub(time.Now())
	q.adjustTime = func(t time.Time) time.Time { return t.Add(shift) }
	q.clock.mu.Lock()
	q.clock.sampled = time.Time{}
	q.clock.sampledMono = time.Time{}
	q.clock.safe = false
	q.clock.mu.Unlock()
	if err := q.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := q.Take(ctx, id, 0, 0)
	if err != nil || got.Usable != 0 {
		t.Fatalf("expired credit remained usable: %+v %v", got, err)
	}
	requireQuotaRow(t, out, "quota.credit_retired", "success", "expired")
	var committed int64
	if err := pool.QueryRow(ctx, `SELECT committed_bytes FROM public.weekly_quota_usage WHERE account_id=$1`, id).Scan(&committed); err != nil || committed != BlockBytes {
		t.Fatalf("retirement refunded committed quota: %d %v", committed, err)
	}
}
