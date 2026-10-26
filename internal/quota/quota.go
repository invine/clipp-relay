// Package quota records observed relay usage and owns accounting eligibility.
package quota

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/persistence"
)

var ErrExhausted = errors.New("weekly quota exhausted")
var ErrTemporary = errors.New("quota temporarily unavailable")

type Quota struct {
	beforeLock          func()
	beforeCommit        func(context.Context, persistence.AccountingTransaction) error
	afterCommit         func() error
	periodic            periodicAccounting
	settlementMu        sync.Mutex
	settlements         map[settlementKey]*settlementState
	accounting          persistence.Accounting
	diagnostics         *diagnostic.Logger
	clock               clockState
	stop                chan struct{}
	done                chan struct{}
	settlementBefore    func(context.Context, persistence.AccountingTransaction) error
	settlementAfter     func(context.Context) error
	eligibilityBoundary func(context.Context) error
	adjustTime          func(time.Time) time.Time
}

// SetAccountingBoundariesForTest controls clock and authority availability at the
// composed HTTP/libp2p test seam. Configure once before serving; callbacks must
// synchronize any later changes. Settlement still uses real PostgreSQL.
func (q *Quota) SetAccountingBoundariesForTest(adjust func(time.Time) time.Time, monotonic func() time.Time, eligibility func(context.Context) error) {
	q.clock.mu.Lock()
	if adjust != nil && !q.clock.sampled.IsZero() {
		q.clock.sampled = adjust(q.clock.sampled)
	}
	q.adjustTime = adjust
	q.clock.mu.Unlock()
	q.periodic.monotonicNow = monotonic
	q.eligibilityBoundary = eligibility
}

// SetDiagnostics connects the process-wide, bounded application logger.
// Configure it before serving requests; quota never retains diagnostic IDs.
func (q *Quota) SetDiagnostics(logger *diagnostic.Logger) { q.diagnostics = logger }

func (q *Quota) emit(event, outcome, reason string, started time.Time) {
	if q.diagnostics == nil {
		return
	}
	e := diagnostic.Event{Event: event, Outcome: outcome, Reason: reason}
	if !started.IsZero() {
		e.Duration = time.Since(started)
	}
	q.diagnostics.Emit(e)
}

func (q *Quota) now() time.Time {
	t := time.Now()
	if q.adjustTime != nil {
		return q.adjustTime(t)
	}
	return t
}
func (q *Quota) dbTime(t time.Time) time.Time {
	if q.adjustTime != nil {
		return q.adjustTime(t)
	}
	return t
}

type clockState struct {
	mu          sync.Mutex
	sampled     time.Time
	sampledMono time.Time
	safe        bool
	recovering  int
	warning     bool
}

func clockIntervalWithin(offset, uncertainty, limit time.Duration) bool {
	return uncertainty >= 0 && uncertainty <= limit && offset >= -limit+uncertainty && offset <= limit-uncertainty
}

// ClockHealth exposes bounded, identity-free clock status for diagnostics.
func (q *Quota) ClockHealth() (safe, warning bool, sampled time.Time) {
	safe = q.clockOK()
	q.clock.mu.Lock()
	defer q.clock.mu.Unlock()
	return safe, q.clock.warning, q.clock.sampled
}

// New composes Quota with the storage-neutral accounting repository.
func New(accounting persistence.Accounting) *Quota {
	q := &Quota{accounting: accounting, stop: make(chan struct{}), done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = q.Probe(ctx)
	cancel()
	go q.probeLoop()
	return q
}

// Close cancels workers; graceful persistence belongs to FlushAllAccounting.
// A faulty dependency cannot indefinitely hold process termination open.
func (q *Quota) Close() {
	q.periodic.mu.Lock()
	cancel, done := q.periodic.cancel, q.periodic.done
	q.periodic.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	close(q.stop)
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	if done != nil {
		select {
		case <-done:
		case <-deadline.C:
			return
		}
	}
	select {
	case <-q.done:
	case <-deadline.C:
	}
}

// NewOperationID produces a cryptographically random stable operation identifier.
func NewOperationID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func weekStart(t time.Time) time.Time {
	u := t.UTC()
	days := (int(u.Weekday()) + 6) % 7
	return time.Date(u.Year(), u.Month(), u.Day()-days, 0, 0, 0, 0, time.UTC)
}
func (q *Quota) probeLoop() {
	defer close(q.done)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-q.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			q.Probe(ctx)
			cancel()
		}
	}
}

// Probe compares a bounded request/response interval with the database clock.
func (q *Quota) Probe(ctx context.Context) error {
	before := q.now()
	var dbTime time.Time
	var err error
	if q.accounting == nil {
		err = ErrTemporary
	} else {
		dbTime, err = q.accounting.ProbeDatabaseTime(ctx)
	}
	after := q.now()
	if err != nil {
		return ErrTemporary
	}
	dbTime = q.dbTime(dbTime)
	uncertainty := after.Sub(before) / 2
	midpoint := before.Add(uncertainty)
	offset := dbTime.Sub(midpoint)
	q.clock.mu.Lock()
	defer q.clock.mu.Unlock()
	previous := q.clock.sampled
	previousMono := q.clock.sampledMono
	if !previous.IsZero() {
		expected := previous.Round(0).Add(time.Since(previousMono))
		drift := after.Round(0).Sub(expected)
		if drift > 5*time.Second || drift < -5*time.Second {
			q.clock.safe = false
			q.clock.recovering = 0
			q.clock.sampled = after
			q.clock.sampledMono = time.Now()
			return ErrTemporary
		}
	}
	q.clock.sampled = after
	q.clock.sampledMono = time.Now()
	q.clock.warning = !clockIntervalWithin(offset, uncertainty, time.Second)
	valid := uncertainty <= time.Second && clockIntervalWithin(offset, uncertainty, 5*time.Second)
	if !valid {
		q.clock.safe = false
		q.clock.recovering = 0
		return ErrTemporary
	}
	if q.clock.safe || previous.IsZero() {
		q.clock.safe = true
		q.clock.recovering = 0
		return nil
	}
	if time.Since(previousMono) >= 50*time.Second && time.Since(previousMono) <= 70*time.Second {
		q.clock.recovering++
	} else {
		q.clock.recovering = 0
	}
	if q.clock.recovering >= 3 {
		q.clock.safe = true
		q.clock.recovering = 0
	}
	if !q.clock.safe {
		return ErrTemporary
	}
	return nil
}
func (q *Quota) clockOK() bool {
	q.clock.mu.Lock()
	defer q.clock.mu.Unlock()
	if q.clock.sampled.IsZero() {
		return false
	}
	age := time.Since(q.clock.sampledMono)
	expectedWall := q.clock.sampled.Round(0).Add(age)
	drift := q.now().Round(0).Sub(expectedWall)
	if drift > 5*time.Second || drift < -5*time.Second {
		q.clock.safe = false
		q.clock.recovering = 0
		return false
	}
	return q.clock.safe && age <= 5*time.Minute
}

func (q *Quota) accountingDiagnostic(event string, err error, started time.Time) {
	// Eligibility runs for every protected admission and periodically for active
	// accounts. Successful checks are routine and provide no useful diagnostic
	// signal; keep refusals and accounting failures visible.
	if event == "quota.eligibility" && err == nil {
		return
	}
	outcome, reason := "success", "none"
	switch {
	case errors.Is(err, ErrExhausted):
		outcome, reason = "refused", "quota_exhausted"
	case errors.Is(err, ErrSettlementUncertain), errors.Is(err, ErrSettlementUnresolved):
		outcome, reason = "uncertain", "settlement_unconfirmed"
	case errors.Is(err, ErrSettlementConflict):
		outcome, reason = "refused", "settlement_conflict"
	case errors.Is(err, ErrUsageRetired):
		outcome, reason = "refused", "usage_retired"
	case err != nil:
		outcome, reason = "failed", "accounting_unavailable"
	}
	q.emit(event, outcome, reason, started)
}
