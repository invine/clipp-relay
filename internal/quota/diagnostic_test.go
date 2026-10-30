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

	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/persistence"
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

func TestQuotaDiagnosticsKnownRollbackAndReconciledSettlement(t *testing.T) {
	q, id, _ := fixture(t)
	out := &diagnosticOutput{}
	log := diagnostic.New(out)
	defer log.Close()
	q.SetDiagnostics(log)
	ctx := context.Background()
	op, _ := newOperationID()
	b := settlement{AccountID: id, WeekStart: weekStart(time.Now()), Sequence: 1, DeltaBytes: 12345, OperationID: op}
	q.SetSettlementHooksForTest(func(context.Context, persistence.AccountingTransaction) error {
		return errors.New("private-rollback-canary")
	}, nil)
	if _, err := q.settle(ctx, b); !errors.Is(err, ErrTemporary) {
		t.Fatalf("rollback: %v", err)
	}
	q.SetSettlementHooksForTest(nil, func(context.Context) error { return errors.New("private-lost-ack-canary") })
	if _, err := q.settle(ctx, b); !errors.Is(err, ErrSettlementUncertain) {
		t.Fatalf("ambiguous: %v", err)
	}
	q.SetSettlementHooksForTest(nil, nil)
	got, err := q.settle(ctx, b)
	if err != nil || got.RecordedBytes != 12345 {
		t.Fatalf("reconcile: %+v %v", got, err)
	}
	requireQuotaRow(t, out, "quota.settlement", "success", "none")
	encoded, _ := json.Marshal(out.rows())
	for _, canary := range []string{id, "private-rollback-canary", "private-lost-ack-canary"} {
		if strings.Contains(string(encoded), canary) {
			t.Fatalf("leaked %s", canary)
		}
	}
}
