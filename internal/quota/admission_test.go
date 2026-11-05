package quota

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"clipp-relay/internal/persistence"
)

type admissionFixture struct {
	capBytes, recorded int64
	limit              int
}

func (f *admissionFixture) ProbeDatabaseTime(context.Context) (time.Time, error) {
	return time.Now(), nil
}
func (f *admissionFixture) ReadAccountingEligibility(context.Context, string) (persistence.AccountingEligibility, error) {
	return persistence.AccountingEligibility{DatabaseNow: time.Now(), CapBytes: f.capBytes, SessionLimit: f.limit, RecordedBytes: f.recorded}, nil
}
func (*admissionFixture) ReadRecordedUsage(context.Context, string, time.Time) (persistence.RecordedUsage, error) {
	return persistence.RecordedUsage{}, nil
}
func (*admissionFixture) StartSettlement(context.Context) (persistence.AccountingOperation, error) {
	return nil, errors.New("settlement is unused by this test")
}

func TestQuotaAdmissionEnforcesLiveCountAndAssessmentState(t *testing.T) {
	q := New(&admissionFixture{capBytes: 1000, limit: 1})
	defer q.Close()
	ctx := context.Background()
	first, err := q.Assess(ctx, "account")
	if err != nil {
		t.Fatal(err)
	}
	installed := 0
	install := func() map[string]int {
		installed++
		return map[string]int{"account": 1}
	}
	if err := q.CommitAdmission(first, SessionChange{Kind: SessionNew}, install); err != nil {
		t.Fatalf("first admission: %v", err)
	}
	if got, known := q.LiveSessionCount("account"); !known || got != 1 {
		t.Fatalf("live sessions=(%d,%v), want (1,true)", got, known)
	}

	atCapacity, err := q.Assess(ctx, "account")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CommitAdmission(atCapacity, SessionChange{Kind: SessionNew}, install); err != ErrSessionLimit {
		t.Fatalf("at-cap admission=%v, want session limit refusal", err)
	}
	if installed != 1 {
		t.Fatalf("refused admission ran installer %d times", installed-1)
	}
	if err := q.CommitAdmission(atCapacity, SessionChange{Kind: SessionRenewal}, func() map[string]int {
		installed++
		return map[string]int{"account": 1}
	}); err != nil {
		t.Fatalf("same-connection renewal at capacity: %v", err)
	}

	q.PublishLiveCounts(map[string]int{"source": 1})
	crossAccount, err := q.Assess(ctx, "account")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CommitAdmission(crossAccount, SessionChange{Kind: SessionReplacement, PreviousAccount: "source"}, install); err != ErrSessionLimit {
		t.Fatalf("cross-account replacement at target capacity=%v, want session limit refusal", err)
	}
	if got, known := q.LiveSessionCount("source"); !known || got != 1 {
		t.Fatalf("refused replacement changed source count=(%d,%v), want (1,true)", got, known)
	}
	if installed != 2 {
		t.Fatalf("refused replacement ran installer %d times", installed-2)
	}

	policyStale, err := q.Assess(ctx, "account")
	if err != nil {
		t.Fatal(err)
	}
	if q.ApplyAccountingPolicy("account", 1000) {
		t.Fatal("policy unexpectedly exhausted")
	}
	if err := q.CommitAdmission(policyStale, SessionChange{Kind: SessionNew}, install); err != ErrTemporary {
		t.Fatalf("assessment after policy update=%v, want temporary refusal", err)
	}
	if installed != 2 {
		t.Fatalf("stale policy assessment ran installer: %d", installed)
	}

	stale, err := q.Assess(ctx, "account")
	if err != nil {
		t.Fatal(err)
	}
	q.ForgetAccounting("account")
	if got, known := q.LiveSessionCount("account"); !known || got != 1 {
		t.Fatalf("usage eviction lost live session count=(%d,%v), want (1,true)", got, known)
	}
	if _, err := q.Assess(ctx, "account"); err != nil {
		t.Fatal(err)
	}
	if err := q.CommitAdmission(stale, SessionChange{Kind: SessionNew}, install); err != ErrTemporary {
		t.Fatalf("assessment from forgotten accounting state=%v, want temporary refusal", err)
	}
}

func TestQuotaAdmissionReplacesSessionsWithoutLosingAccountCounts(t *testing.T) {
	q := New(&admissionFixture{capBytes: 1000, limit: 2})
	defer q.Close()
	ctx := context.Background()
	install := func(counts map[string]int) func() map[string]int {
		return func() map[string]int { return counts }
	}
	first, err := q.Assess(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CommitAdmission(first, SessionChange{Kind: SessionNew}, install(map[string]int{"target": 1})); err != nil {
		t.Fatal(err)
	}
	replacement, err := q.Assess(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CommitAdmission(replacement, SessionChange{Kind: SessionReplacement, PreviousAccount: "target"}, install(map[string]int{"target": 1})); err != nil {
		t.Fatalf("same-account replacement at cap: %v", err)
	}
	if got, known := q.LiveSessionCount("target"); !known || got != 1 {
		t.Fatalf("same-account replacement count=(%d,%v), want (1,true)", got, known)
	}

	q.PublishLiveCounts(map[string]int{"source": 1})
	crossAccount, err := q.Assess(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.CommitAdmission(crossAccount, SessionChange{Kind: SessionReplacement, PreviousAccount: "source"}, install(map[string]int{"source": 0, "target": 2})); err != nil {
		t.Fatalf("cross-account replacement with target capacity: %v", err)
	}
	if got, known := q.LiveSessionCount("target"); !known || got != 2 {
		t.Fatalf("cross-account target count=(%d,%v), want (2,true)", got, known)
	}
	if got, known := q.LiveSessionCount("source"); !known || got != 0 {
		t.Fatalf("released source count=(%d,%v), want known zero", got, known)
	}

	q.PublishLiveCounts(map[string]int{"target": 0})
	if got, known := q.LiveSessionCount("target"); !known || got != 0 {
		t.Fatalf("disconnected account count=(%d,%v), want known zero", got, known)
	}
}

func TestKnownZeroLiveCountSamplesAreBoundedAndRecent(t *testing.T) {
	q := New(&admissionFixture{capBytes: 1000, limit: 1})
	defer q.Close()
	for i := 0; i <= maxKnownZeroLiveCounts; i++ {
		q.PublishLiveCounts(map[string]int{fmt.Sprintf("account-%05d", i): 0})
	}
	if _, known := q.LiveSessionCount("account-00000"); known {
		t.Fatal("oldest zero sample was not evicted")
	}
	if got, known := q.LiveSessionCount(fmt.Sprintf("account-%05d", maxKnownZeroLiveCounts)); !known || got != 0 {
		t.Fatalf("newest zero sample=(%d,%v), want known zero", got, known)
	}
}
