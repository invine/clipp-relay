package postgres_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/persistence/postgres"
)

func TestAccountingReceiptIsAtomicAndRejectsStaleOrConflictingBatches(t *testing.T) {
	ctx := context.Background()
	path := os.Getenv("CLIPP_TEST_SERVING_CONFIG")
	if path == "" {
		t.Skip("disposable PostgreSQL fixture not configured")
	}
	c, err := config.Load(path)
	if err != nil || c.Database.Host != "localhost" || !strings.HasPrefix(c.Database.Name, "clipp_ticket02_") {
		t.Fatalf("unsafe fixture: %v", err)
	}
	m, err := c.ReadMaterial()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.NewPool(ctx, c, m, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	accountID := testUUID(t)
	if _, err := pool.Exec(ctx, `INSERT INTO public.accounts(id,issuer,subject,email,email_verified,validated_at,created_at,last_portal_login_at,status,plan_id) VALUES($1,'https://accounts.google.com',$1,$2,true,clock_timestamp(),clock_timestamp(),clock_timestamp(),'Active','6dd09395-51a0-451c-96b3-716e6038e870')`, accountID, accountID+"@example.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.weekly_relay_usage WHERE account_id=$1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.accounts WHERE id=$1`, accountID)
	})

	accounting := postgres.New(database.NewRuntime(pool))
	var week time.Time
	if err := pool.QueryRow(ctx, `SELECT date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&week); err != nil {
		t.Fatal(err)
	}
	first := persistence.UsageSettlement{AccountID: accountID, WeekStart: week, OperationID: testUUID(t), Sequence: 1, DeltaBytes: 101}
	if got, err := applyUsageSettlement(ctx, accounting, first, true); err != nil || got.RecordedBytes != 101 || got.Sequence != 1 {
		t.Fatalf("first receipt = %+v, %v", got, err)
	}
	if got, err := applyUsageSettlement(ctx, accounting, first, true); err != nil || got.RecordedBytes != 101 || got.OperationID != first.OperationID {
		t.Fatalf("identical replay = %+v, %v", got, err)
	}
	for name, invalid := range map[string]persistence.UsageSettlement{
		"same operation changed payload": {AccountID: accountID, WeekStart: week, OperationID: first.OperationID, Sequence: 1, DeltaBytes: 102},
		"sequence gap":                   {AccountID: accountID, WeekStart: week, OperationID: testUUID(t), Sequence: 3, DeltaBytes: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := applyUsageSettlement(ctx, accounting, invalid, true); err != persistence.ErrConflict {
				t.Fatalf("invalid receipt error = %v", err)
			}
		})
	}
	second := persistence.UsageSettlement{AccountID: accountID, WeekStart: week, OperationID: testUUID(t), Sequence: 2, DeltaBytes: 7}
	if got, err := applyUsageSettlement(ctx, accounting, second, true); err != nil || got.RecordedBytes != 108 || got.Sequence != 2 {
		t.Fatalf("second receipt = %+v, %v", got, err)
	}
	if _, err := applyUsageSettlement(ctx, accounting, first, true); err != persistence.ErrConflict {
		t.Fatalf("stale retry error = %v", err)
	}
	if got, err := accounting.ReadRecordedUsage(ctx, accountID, week); err != nil || got.RecordedBytes != 108 || got.Sequence != 2 || got.OperationID != second.OperationID {
		t.Fatalf("confirmed receipt = %+v, %v", got, err)
	}

	rollbackWeek := week.AddDate(0, 0, -7)
	rolledBack := persistence.UsageSettlement{AccountID: accountID, WeekStart: rollbackWeek, OperationID: testUUID(t), Sequence: 1, DeltaBytes: 55}
	if _, err := applyUsageSettlement(ctx, accounting, rolledBack, false); err != nil {
		t.Fatal(err)
	}
	if got, err := accounting.ReadRecordedUsage(ctx, accountID, rollbackWeek); err != nil || got.RecordedBytes != 0 || got.Sequence != 0 || got.OperationID != "" {
		t.Fatalf("rolled-back receipt became visible: %+v, %v", got, err)
	}

	overflowWeek := week.AddDate(0, 0, -14)
	if _, err := pool.Exec(ctx, `INSERT INTO public.weekly_relay_usage(account_id,week_start,recorded_bytes,sequence,latest_delta_bytes) VALUES($1,$2,$3,0,0)`, accountID, overflowWeek, int64(^uint64(0)>>1)-9); err != nil {
		t.Fatal(err)
	}
	overflow := persistence.UsageSettlement{AccountID: accountID, WeekStart: overflowWeek, OperationID: testUUID(t), Sequence: 1, DeltaBytes: 10}
	if _, err := applyUsageSettlement(ctx, accounting, overflow, true); err != persistence.ErrConflict {
		t.Fatalf("overflow error = %v", err)
	}
	if got, err := accounting.ReadRecordedUsage(ctx, accountID, overflowWeek); err != nil || got.RecordedBytes != int64(^uint64(0)>>1)-9 || got.Sequence != 0 || got.OperationID != "" {
		t.Fatalf("overflow partially changed receipt: %+v, %v", got, err)
	}
}

func applyUsageSettlement(ctx context.Context, accounting persistence.Accounting, batch persistence.UsageSettlement, commit bool) (persistence.RecordedUsage, error) {
	op, err := accounting.StartSettlement(ctx)
	if err != nil {
		return persistence.RecordedUsage{}, err
	}
	defer op.Close()
	tx, err := op.BeginAccounting(ctx)
	if err != nil {
		return persistence.RecordedUsage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.LockSettlementAccount(ctx, batch.AccountID); err != nil {
		return persistence.RecordedUsage{}, err
	}
	if _, err = tx.DatabaseTime(ctx); err != nil {
		return persistence.RecordedUsage{}, err
	}
	receipt, err := tx.SettleUsage(ctx, batch)
	if err != nil {
		return receipt, err
	}
	if !commit {
		return receipt, nil
	}
	return receipt, tx.Commit(ctx)
}

func testUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
