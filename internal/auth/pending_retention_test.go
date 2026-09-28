package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/auth"
)

// Maintenance is observed at the account interface: an abandoned Pending
// identity can register afresh and its only durable trace is detached audit.
func TestAbandonedPendingExpiresWithoutIdentityOrJournal(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	d.subject = "abandoned-pending"
	s := auth.New(db.Pool, c, m, d.endpoints())
	flow, binding := start(t, s, nil)
	w := complete(s, flow, binding)
	if w.Code != 303 {
		t.Fatalf("setup: %d", w.Code)
	}
	cookie := sessionFrom(w)
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET last_portal_login_at=clock_timestamp()-interval '89 days' WHERE subject=$1`, d.subject); err != nil {
		t.Fatal(err)
	}
	flow, binding = start(t, s, nil)
	w = complete(s, flow, binding)
	if w.Code != 303 {
		t.Fatalf("successful Google login did not refresh Pending: %d", w.Code)
	}
	var refreshed bool
	if err := db.Pool.QueryRow(context.Background(), `SELECT last_portal_login_at>clock_timestamp()-interval '1 minute' FROM public.accounts WHERE subject=$1`, d.subject).Scan(&refreshed); err != nil || !refreshed {
		t.Fatalf("Pending login clock was not refreshed: %v", err)
	}
	cookie = sessionFrom(w)
	var oldID string
	if err := db.Pool.QueryRow(context.Background(), `UPDATE public.accounts SET last_portal_login_at=clock_timestamp()-interval '91 days' WHERE subject=$1 RETURNING id`, d.subject).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	stale := portalRequest(s, "GET", "/", cookie, nil, "")
	if strings.Contains(stale.Body.String(), "Your account is waiting for approval.") {
		t.Fatal("expired Pending session remained visible before cleanup")
	}
	var stillExpired bool
	if err := db.Pool.QueryRow(context.Background(), `SELECT last_portal_login_at<clock_timestamp()-interval '90 days' FROM public.accounts WHERE id=$1`, oldID).Scan(&stillExpired); err != nil || !stillExpired {
		t.Fatalf("generic portal traffic refreshed Pending: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Maintain(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var remaining int
		if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.accounts WHERE id=$1`, oldID).Scan(&remaining); err != nil {
			t.Fatal(err)
		}
		if remaining == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	var remaining, audit, journal int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.accounts WHERE id=$1`, oldID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events WHERE account_id=$1 AND event='pending_expired'`, oldID).Scan(&audit); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM public.deletion_operations WHERE account_id=$1`, oldID).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 || audit != 1 || journal != 0 {
		t.Fatalf("expired Pending state: account=%d audit=%d journal=%d", remaining, audit, journal)
	}
	flow, binding = start(t, s, nil)
	if w := complete(s, flow, binding); w.Code != 303 {
		t.Fatalf("fresh registration: %d", w.Code)
	}
	var newID string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, d.subject).Scan(&newID); err != nil || newID == oldID {
		t.Fatalf("fresh identity mapping: %s %v", newID, err)
	}
}
