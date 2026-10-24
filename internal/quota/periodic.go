package quota

import (
	"context"
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

const AccountingCadence = time.Second
const DefaultAccountingFailureDeadline = 3 * time.Second
const maxAccountingAccounts = 6000

type AccountingEligibility struct {
	RecordedBytes, EffectiveBytes, CapBytes int64
	WeekStart                               time.Time
	Revision                                uint64
}
type ClosureOutcome struct {
	AccountID, Reason string
	Revision          uint64
}
type observedWeek struct {
	pending     int64
	flight      *Settlement
	recorded    RecordedUsage
	initialized bool
}
type observedAccount struct {
	sessions    int
	weeks       map[time.Time]*observedWeek
	generation  int64
	healthy     time.Time
	revision    uint64
	blocked     bool
	invalidated bool
	reason      string
	busy        bool
}
type periodicAccounting struct {
	monotonicNow func() time.Time
	mu           sync.Mutex
	accounts     map[string]*observedAccount
	started      bool
	cancel       context.CancelFunc
	done         chan struct{}
	close        func(ClosureOutcome)
	deadline     time.Duration
}

func (q *Quota) observedAccountLocked(id string) *observedAccount {
	if q.periodic.accounts == nil {
		q.periodic.accounts = make(map[string]*observedAccount)
	}
	a := q.periodic.accounts[id]
	if a == nil && len(q.periodic.accounts) < maxAccountingAccounts {
		a = &observedAccount{weeks: make(map[time.Time]*observedWeek)}
		q.periodic.accounts[id] = a
	}
	return a
}

// Observe records processing-time attribution without database or connection I/O.
// Accounts are registered by protected Eligibility before Relay can report bytes.
func (q *Quota) Observe(id string, n int64) error {
	if n < 0 {
		return ErrSettlementConflict
	}
	if n == 0 {
		return nil
	}
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	a := q.periodic.accounts[id]
	if a == nil {
		return ErrTemporary
	}
	week := weekStart(q.now())
	w := a.weeks[week]
	if w == nil {
		w = &observedWeek{}
		a.weeks[week] = w
	}
	if n > math.MaxInt64-w.pending {
		return ErrTemporary
	}
	w.pending += n
	return nil
}

// Eligibility is an authoritative admission assessment. Its caller holds Auth's
// account guard and admitted database budget; it never reenters that guard.
func (q *Quota) Eligibility(ctx context.Context, id string, generation int64) (AccountingEligibility, error) {
	var result AccountingEligibility
	if !q.clockOK() {
		return result, ErrTemporary
	}
	var status string
	var actual int64
	var now time.Time
	err := q.runtime.QueryRow(ctx, `SELECT clock_timestamp(),a.status,a.credential_generation,COALESCE(a.weekly_bytes_override,p.weekly_bytes) FROM public.accounts a JOIN public.quota_plans p ON p.id=a.plan_id WHERE a.id=$1 AND a.deleted_at IS NULL AND a.deletion_started_at IS NULL`, id).Scan(&now, &status, &actual, &result.CapBytes)
	if err != nil || status != "Active" || actual != generation {
		return result, ErrTemporary
	}
	result.WeekStart = weekStart(q.dbTime(now))
	durable, err := q.RecordedUsage(ctx, id, result.WeekStart)
	if err != nil {
		return result, err
	}
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	a := q.observedAccountLocked(id)
	if a == nil {
		return result, ErrTemporary
	}
	a.generation = generation
	a.invalidated = false
	for week, v := range a.weeks {
		if week != result.WeekStart && v.pending == 0 && v.flight == nil {
			delete(a.weeks, week)
		}
	}
	w := a.weeks[result.WeekStart]
	if w == nil {
		w = &observedWeek{}
		a.weeks[result.WeekStart] = w
	}
	w.recorded = durable
	w.initialized = true
	result.RecordedBytes = durable.RecordedBytes
	result.EffectiveBytes = durable.RecordedBytes
	extra := w.pending
	if w.flight != nil && !(durable.OperationID == w.flight.OperationID && durable.Sequence == w.flight.Sequence) {
		if w.flight.DeltaBytes > math.MaxInt64-extra {
			return result, ErrTemporary
		}
		extra += w.flight.DeltaBytes
	}
	if extra > math.MaxInt64-result.EffectiveBytes {
		return result, ErrTemporary
	}
	result.EffectiveBytes += extra
	pending := hasObserved(a)
	// Only an idle authoritative read can restore write health.
	if !pending {
		a.healthy = q.accountingNow()
		a.blocked = false
		a.reason = ""
	}
	if a.healthy.IsZero() {
		a.healthy = q.accountingNow()
	}
	result.Revision = a.revision
	if result.EffectiveBytes >= result.CapBytes {
		return result, ErrExhausted
	}
	if a.blocked || q.accountingNow().Sub(a.healthy) >= q.accountingDeadlineLocked() {
		return result, ErrTemporary
	}
	return result, nil
}
func hasObserved(a *observedAccount) bool {
	for _, w := range a.weeks {
		if w.pending != 0 || w.flight != nil {
			return true
		}
	}
	return false
}
func (q *Quota) accountingDeadlineLocked() time.Duration {
	if q.periodic.deadline > 0 {
		return q.periodic.deadline
	}
	return DefaultAccountingFailureDeadline
}

// AccountingCurrent lets Relay reject an admission whose accounting authority
// was invalidated between assessment and physical session installation.
func (q *Quota) AccountingCurrent(id string, revision uint64) bool {
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	a := q.periodic.accounts[id]
	return a != nil && !a.blocked && !a.invalidated && a.revision == revision
}

// FlushAccounting attempts each original observation week once. An immutable
// failed batch remains queued for the same retry, independent of session life.
func (q *Quota) FlushAccounting(ctx context.Context, id string) error {
	q.periodic.mu.Lock()
	a := q.periodic.accounts[id]
	if a == nil {
		q.periodic.mu.Unlock()
		return nil
	}
	if a.busy {
		q.periodic.mu.Unlock()
		return ErrTemporary
	}
	a.busy = true
	weeks := make([]time.Time, 0, len(a.weeks))
	for week, w := range a.weeks {
		if w.pending > 0 || w.flight != nil {
			weeks = append(weeks, week)
		}
	}
	q.periodic.mu.Unlock()
	defer func() { q.periodic.mu.Lock(); a.busy = false; q.periodic.mu.Unlock() }()
	sort.Slice(weeks, func(i, j int) bool { return weeks[i].Before(weeks[j]) })
	for _, week := range weeks {
		q.periodic.mu.Lock()
		w := a.weeks[week]
		initialized := w.initialized
		q.periodic.mu.Unlock()
		if !initialized {
			usage, err := q.RecordedUsage(ctx, id, week)
			if err != nil {
				return err
			}
			q.periodic.mu.Lock()
			w.recorded = usage
			w.initialized = true
			q.periodic.mu.Unlock()
		}
		q.periodic.mu.Lock()
		if w.flight == nil {
			op, err := NewOperationID()
			if err != nil {
				q.periodic.mu.Unlock()
				return ErrTemporary
			}
			if w.recorded.Sequence == math.MaxInt64 {
				q.periodic.mu.Unlock()
				return ErrSettlementConflict
			}
			w.flight = &Settlement{AccountID: id, WeekStart: week, OperationID: op, DeltaBytes: w.pending, Sequence: w.recorded.Sequence + 1}
			w.pending = 0
		}
		batch := *w.flight
		q.periodic.mu.Unlock()
		usage, err := q.Settle(ctx, batch)
		if err != nil {
			return err
		}
		q.periodic.mu.Lock()
		w.recorded = usage
		w.flight = nil
		// Progress in a newer week cannot hide older unresolved work.
		older := false
		for other, v := range a.weeks {
			if other.Before(week) && (v.flight != nil || v.pending > 0) {
				older = true
			}
		}
		if !older {
			a.healthy = q.accountingNow()
			a.blocked = false
			a.reason = ""
		}
		q.periodic.mu.Unlock()
	}
	return nil
}

// StartAccounting starts four fixed workers and an independent cutoff ticker.
// The callback must detach synchronously without waiting for account guards;
// connection I/O belongs to Relay. Configure before serving.
func (q *Quota) StartAccounting(closeAccount func(ClosureOutcome)) error {
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	if q.periodic.started || closeAccount == nil || q.settlementProtection == nil {
		return errors.New("accounting requires complete protection and one closure consumer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	q.periodic.started = true
	q.periodic.cancel = cancel
	q.periodic.done = make(chan struct{})
	q.periodic.close = closeAccount
	go q.accountingLoop(ctx)
	return nil
}
func (q *Quota) accountingLoop(ctx context.Context) {
	defer close(q.periodic.done)
	jobs := make(chan string, 64)
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-jobs:
					_ = q.FlushAccounting(ctx, id)
					q.periodic.mu.Lock()
					a := q.periodic.accounts[id]
					generation := int64(0)
					if a != nil {
						generation = a.generation
					}
					q.periodic.mu.Unlock()
					// Background reads and decisions share Auth protection with admission.
					q.runtime.WithUnit(ctx, func(unit context.Context) error {
						q.settlementProtection.WithAccountGuards(unit, []string{id}, func(guard context.Context) {
							_, err := q.Eligibility(guard, id, generation)
							if errors.Is(err, ErrExhausted) {
								q.cutoff(id, "quota_exhausted")
							}
							q.periodic.mu.Lock()
							if a := q.periodic.accounts[id]; a != nil && a.sessions == 0 && !a.busy && !hasObserved(a) {
								delete(q.periodic.accounts, id)
							}
							q.periodic.mu.Unlock()
						})
						return nil
					})
				}
			}
		}()
	}
	cadence := time.NewTicker(AccountingCadence)
	deadline := time.NewTicker(50 * time.Millisecond)
	defer cadence.Stop()
	defer deadline.Stop()
	defer workers.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case <-cadence.C:
			q.periodic.mu.Lock()
			ids := make([]string, 0, len(q.periodic.accounts))
			for id := range q.periodic.accounts {
				ids = append(ids, id)
			}
			q.periodic.mu.Unlock()
			for _, id := range ids {
				select {
				case jobs <- id:
				default:
				}
			}
		case <-deadline.C:
			q.CheckAccountingHealth()
		}
	}
}

// CheckAccountingHealth runs independently of database workers. Elapsed time is
// monotonic even if the wall clock changes.
func (q *Quota) CheckAccountingHealth() {
	q.periodic.mu.Lock()
	ids := []string{}
	for id, a := range q.periodic.accounts {
		if !a.blocked && !a.healthy.IsZero() && q.accountingNow().Sub(a.healthy) >= q.accountingDeadlineLocked() {
			ids = append(ids, id)
		}
	}
	q.periodic.mu.Unlock()
	for _, id := range ids {
		q.cutoffChecked(id, "accounting_unavailable", true)
	}
}
func (q *Quota) cutoff(id, reason string) { q.cutoffChecked(id, reason, false) }
func (q *Quota) cutoffChecked(id, reason string, expiredOnly bool) {
	q.periodic.mu.Lock()
	a := q.periodic.accounts[id]
	if a == nil || a.blocked || expiredOnly && (a.healthy.IsZero() || q.accountingNow().Sub(a.healthy) < q.accountingDeadlineLocked()) {
		q.periodic.mu.Unlock()
		return
	}
	a.blocked = true
	a.reason = reason
	a.revision++
	out := ClosureOutcome{AccountID: id, Reason: reason, Revision: a.revision}
	callback := q.periodic.close
	q.periodic.mu.Unlock()
	if callback != nil {
		callback(out)
	}
}

// ForgetAccounting follows the final durable deletion seal, never disconnect or
// credential revocation. Lifecycle callers must first attempt a bounded flush.
func (q *Quota) ForgetAccounting(id string) {
	q.periodic.mu.Lock()
	delete(q.periodic.accounts, id)
	q.periodic.mu.Unlock()
}

// ClosureCurrent fences delayed outcomes after accounting recovers.
func (q *Quota) ClosureCurrent(out ClosureOutcome) bool {
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	a := q.periodic.accounts[out.AccountID]
	return a != nil && a.blocked && a.revision == out.Revision
}

func (q *Quota) accountingNow() time.Time {
	if q.periodic.monotonicNow != nil {
		return q.periodic.monotonicNow()
	}
	return time.Now()
}

// AccountingSessions records only physical registry occupancy, never policy.
// Relay calls it with its registry mutex held after installation/removal.
func (q *Quota) AccountingSessions(id string, count int) {
	q.periodic.mu.Lock()
	if a := q.periodic.accounts[id]; a != nil {
		a.sessions = count
	}
	q.periodic.mu.Unlock()
}

// AccountingFailed fences reporter counter failures through the same immediate
// closure path as background health failure, without connection I/O.
func (q *Quota) AccountingFailed(id string) { q.cutoff(id, "accounting_unavailable") }

// ConfigureAccountingFailureDeadline installs restart-scoped policy before workers
// start. Invalid durations never disable the independent cutoff.
func (q *Quota) ConfigureAccountingFailureDeadline(deadline time.Duration) error {
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	if deadline < AccountingCadence || q.periodic.started {
		return errors.New("accounting failure deadline must be at least 1s and configured before serving")
	}
	q.periodic.deadline = deadline
	return nil
}

// ApplyAccountingPolicy runs under Auth account protection after a policy
// mutation. It fences prior assessments and preserves every observed byte.
// The caller owns synchronous physical detachment and deferred connection I/O.
func (q *Quota) ApplyAccountingPolicy(id string, cap int64, invalidate bool) bool {
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	a := q.periodic.accounts[id]
	if a == nil {
		return cap <= 0
	}
	a.revision++
	a.invalidated = a.invalidated || invalidate
	exhausted := cap <= 0
	if w := a.weeks[weekStart(q.now())]; w != nil {
		effective := w.recorded.RecordedBytes
		extra := w.pending
		if w.flight != nil && !(w.recorded.OperationID == w.flight.OperationID && w.recorded.Sequence == w.flight.Sequence) {
			if w.flight.DeltaBytes > math.MaxInt64-extra {
				exhausted = true
			} else {
				extra += w.flight.DeltaBytes
			}
		}
		exhausted = exhausted || extra > math.MaxInt64-effective || effective+extra >= cap
	}
	if exhausted {
		a.blocked = true
		a.reason = "quota_exhausted"
	} else if a.reason == "quota_exhausted" {
		a.blocked = false
		a.reason = ""
	}
	return exhausted
}
