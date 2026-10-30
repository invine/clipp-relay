package auth_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"
	"time"
)

func testOperationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// observeAndConfirm records usage through Quota's consumer-facing operation.
func observeAndConfirm(t *testing.T, p protectedPortal, bytes int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := p.quota.Eligibility(ctx, p.account); err != nil {
		t.Fatalf("initialize quota eligibility: %v", err)
	}
	if err := p.quota.Observe(p.account, bytes); err != nil {
		t.Fatalf("observe quota usage: %v", err)
	}
	if err := p.quota.FlushAccounting(ctx, p.account); err != nil {
		t.Fatalf("flush quota usage: %v", err)
	}
}

// seedRecordedWeek is reserved for historical fixture setup that cannot be
// produced by a live observation in the current Weekly Quota Window.
func seedRecordedWeek(t *testing.T, p protectedPortal, week time.Time, bytes int64) {
	t.Helper()
	_, err := p.runtime.Pool.Exec(context.Background(), `INSERT INTO public.weekly_relay_usage(account_id,week_start,recorded_bytes,sequence,latest_operation_id,latest_delta_bytes) VALUES($1,$2,$3,1,'386dfe14-2f37-45e7-ace8-0cbcc3851469',$3) ON CONFLICT(account_id,week_start) DO UPDATE SET recorded_bytes=EXCLUDED.recorded_bytes,sequence=EXCLUDED.sequence,latest_operation_id=EXCLUDED.latest_operation_id,latest_delta_bytes=EXCLUDED.latest_delta_bytes`, p.account, week, bytes)
	if err != nil {
		t.Fatalf("seed historical quota usage: %v", err)
	}
}
