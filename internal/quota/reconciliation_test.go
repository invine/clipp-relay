package quota

import (
	"context"
	"testing"
	"time"
)

func TestCommittedSessionCapReconcilesBeforeAdmissionCanUseOldAssessment(t *testing.T) {
	store := &admissionFixture{capBytes: 1000, limit: 5}
	q := New(store)
	defer q.Close()
	ctx := context.Background()
	old, err := q.Assess(ctx, "account")
	if err != nil {
		t.Fatal(err)
	}
	q.PublishLiveCounts(map[string]int{"account": 3})
	store.limit = 3
	var observed int
	var retired bool
	q.SetSessionReconciler(func(account string, decide func(int) (bool, string)) error {
		if account != "account" {
			t.Fatalf("reconciled account=%q", account)
		}
		observed = 3 // The exact count is supplied under the Relay registry lock.
		retired, _ = decide(observed)
		return nil
	})
	if reason, err := q.ApplyCommittedLimits("account", 1000, 3); err != nil || reason != "" {
		t.Fatalf("cap equal to occupancy reason=%q err=%v", reason, err)
	}
	if observed != 3 || retired {
		t.Fatalf("observed=%d retired=%v, want count three preserved", observed, retired)
	}
	if err := q.CommitAdmission(old, SessionChange{Kind: SessionNew}, func() map[string]int {
		t.Fatal("stale assessment ran its installer")
		return nil
	}); err != ErrTemporary {
		t.Fatalf("old assessment result=%v, want temporary refusal", err)
	}
	fresh, err := q.Assess(ctx, "account")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CommitAdmission(fresh, SessionChange{Kind: SessionNew}, func() map[string]int {
		t.Fatal("fresh over-cap admission ran its installer")
		return nil
	}); err != ErrSessionLimit {
		t.Fatalf("fresh at-cap admission=%v, want session limit", err)
	}
}

func TestCommittedWeeklyLimitRetiresOnUnsettledUsage(t *testing.T) {
	store := &admissionFixture{capBytes: 1000, limit: 5}
	q := New(store)
	defer q.Close()
	ctx := context.Background()
	if _, err := q.Eligibility(ctx, "account"); err != nil {
		t.Fatal(err)
	}
	if err := q.Observe("account", 150); err != nil {
		t.Fatal(err)
	}
	var retired bool
	q.SetSessionReconciler(func(_ string, decide func(int) (bool, string)) error {
		var reason string
		retired, reason = decide(2)
		if reason != "quota_exhausted" {
			t.Fatalf("retirement reason=%q, want quota_exhausted", reason)
		}
		return nil
	})
	store.capBytes = 100
	reason, err := q.ApplyCommittedLimits("account", 100, 5)
	if err != nil || reason != "quota_exhausted" || !retired {
		t.Fatalf("reason=%q retired=%v err=%v", reason, retired, err)
	}
}

func TestCommittedLimitsFenceFirstAssessmentEvenWithoutAccountState(t *testing.T) {
	store := &admissionFixture{capBytes: 1000, limit: 5}
	q := New(store)
	defer q.Close()
	before := q.periodic.policyEpoch
	q.SetSessionReconciler(func(_ string, decide func(int) (bool, string)) error {
		retire, _ := decide(0)
		if retire {
			t.Fatal("zero count retired under a positive policy")
		}
		return nil
	})
	if _, err := q.ApplyCommittedLimits("new-account", 1000, 5); err != nil {
		t.Fatal(err)
	}
	if q.periodic.policyEpoch <= before {
		t.Fatal("confirmed policy application did not advance the policy epoch")
	}
	if _, err := q.Eligibility(context.Background(), "new-account"); err != nil {
		t.Fatalf("fresh first assessment: %v", err)
	}
	if got := weekStart(time.Now()); got.IsZero() {
		t.Fatal("unexpected zero weekly window")
	}
}
