package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"clipp-relay/internal/persistence"
)

type controlledMaintenance struct {
	health      persistence.CleanupHealth
	coverage    persistence.PepperCoverage
	healthErr   error
	coverageErr error
	recordErr   error
	batchErr    map[persistence.CleanupOperation]error
	results     map[persistence.CleanupOperation]persistence.CleanupBatchResult
	calls       []persistence.CleanupBatch
	breaches    []struct {
		active bool
		age    int64
	}
}

func (m *controlledMaintenance) CleanupHealth(context.Context) (persistence.CleanupHealth, error) {
	return m.health, m.healthErr
}
func (m *controlledMaintenance) RecordCleanupBreach(_ context.Context, active bool, age int64) error {
	m.breaches = append(m.breaches, struct {
		active bool
		age    int64
	}{active, age})
	return m.recordErr
}
func (m *controlledMaintenance) CleanupBatch(_ context.Context, batch persistence.CleanupBatch) (persistence.CleanupBatchResult, error) {
	m.calls = append(m.calls, batch)
	return m.results[batch.Operation], m.batchErr[batch.Operation]
}
func (m *controlledMaintenance) PepperCoverage(context.Context) (persistence.PepperCoverage, error) {
	return m.coverage, m.coverageErr
}

func TestCleanupPassKeepsBoundedFairBatchOperationsAfterFailure(t *testing.T) {
	failure := errors.New("maintenance unavailable")
	repo := &controlledMaintenance{
		batchErr: map[persistence.CleanupOperation]error{persistence.CleanupRetainedUsage: failure},
		results:  map[persistence.CleanupOperation]persistence.CleanupBatchResult{},
	}
	s := &Server{maintenance: repo, cleanupAccountCursors: make([]string, cleanupOperationCount)}
	more, err := s.cleanupPass(context.Background())
	if !errors.Is(err, failure) {
		t.Fatalf("cleanup error = %v, want first batch failure", err)
	}
	if len(repo.calls) != cleanupOperationCount {
		t.Fatalf("processed %d operations, want all %d despite a partial failure", len(repo.calls), cleanupOperationCount)
	}
	if repo.calls[0].Operation != persistence.CleanupRetainedUsage || repo.calls[len(repo.calls)-2].Operation != persistence.CleanupDeletedAccounts || repo.calls[len(repo.calls)-1].Operation != persistence.CleanupExpiredPendingAccounts {
		t.Fatalf("unexpected initial/parent order: %+v", repo.calls)
	}
	for _, call := range repo.calls {
		want := int64(cleanupBatchSize)
		if call.Operation == persistence.CleanupExpiredPendingAccounts {
			want /= 2
		}
		if call.Limit != want {
			t.Fatalf("operation %v limit = %d, want bounded batch %d", call.Operation, call.Limit, cleanupBatchSize)
		}
	}
	if more {
		t.Fatal("empty controlled batches reported more work")
	}
}

func TestCleanupPassRotatesFairClassesAndCarriesCursors(t *testing.T) {
	repo := &controlledMaintenance{batchErr: map[persistence.CleanupOperation]error{}, results: map[persistence.CleanupOperation]persistence.CleanupBatchResult{
		persistence.CleanupHiddenPortalSessions: {Removed: 20, Candidates: 16, Take: 13, NextCursor: "next-account"},
	}}
	s := &Server{maintenance: repo, cleanupAccountCursors: make([]string, cleanupOperationCount)}
	more, err := s.cleanupPass(context.Background())
	if err != nil || !more {
		t.Fatalf("first cleanup pass = more:%v err:%v, want fair-work continuation", more, err)
	}
	var hidden persistence.CleanupBatch
	for _, call := range repo.calls {
		if call.Operation == persistence.CleanupHiddenPortalSessions {
			hidden = call
		}
	}
	if hidden.Cursor != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("initial fair cursor = %q", hidden.Cursor)
	}
	repo.calls = nil
	_, err = s.cleanupPass(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if repo.calls[0].Operation == persistence.CleanupRetainedUsage {
		t.Fatal("second pass did not rotate the starting retention class")
	}
	for _, call := range repo.calls {
		if call.Operation == persistence.CleanupHiddenPortalSessions && call.Cursor != "next-account" {
			t.Fatalf("fair cursor = %q, want persisted cursor", call.Cursor)
		}
	}
}

func TestCleanupSignalsAndPepperCoverageKeepPolicyInAuth(t *testing.T) {
	repo := &controlledMaintenance{
		health:   persistence.CleanupHealth{OldestAge: 25 * time.Hour, BreachEpisodes: 7},
		coverage: persistence.PepperCoverage{PortalSessions: []uint64{4}, RetainedUsage: []uint64{9}},
		batchErr: map[persistence.CleanupOperation]error{}, results: map[persistence.CleanupOperation]persistence.CleanupBatchResult{},
	}
	s := &Server{maintenance: repo, Peppers: map[uint64][]byte{4: []byte("portal")}}
	s.cleanupCompleted.Store(3)
	s.cleanupFailed.Store(2)
	signals, err := s.SampleCleanupSignals(context.Background())
	if err != nil || !signals.Warning || !signals.Critical || !signals.Breach || signals.BreachEpisodes != 7 || signals.Completed != 3 || signals.Failed != 2 {
		t.Fatalf("cleanup signals = %+v, %v", signals, err)
	}
	if err := s.ValidatePepperCoverage(context.Background()); !errors.Is(err, ErrRetainedPepperUnavailable) {
		t.Fatalf("missing retained-usage pepper error = %v", err)
	}
	s.Peppers[9] = []byte("retained")
	if err := s.ValidatePepperCoverage(context.Background()); err != nil {
		t.Fatalf("complete key coverage rejected: %v", err)
	}
	if err := s.recordCleanupBreach(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.breaches) != 1 || !repo.breaches[0].active || repo.breaches[0].age != int64(25*60*60) {
		t.Fatalf("persisted cleanup breach = %+v", repo.breaches)
	}
}

func TestPepperCoverageUnavailableDoesNotLookLikeNoDependencies(t *testing.T) {
	repo := &controlledMaintenance{coverageErr: errors.New("backend unavailable")}
	s := &Server{maintenance: repo}
	if err := s.ValidatePepperCoverage(context.Background()); err == nil || err.Error() != "pepper coverage unavailable" {
		t.Fatalf("unavailable coverage error = %v", err)
	}
}
