// Package quota commits conservative account-week credit before it is made usable.
package quota

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const BlockBytes int64 = 65536

var ErrExhausted = errors.New("weekly quota exhausted")
var ErrTemporary = errors.New("quota temporarily unavailable")

// Result reports durable Quota Committed and remaining process-local credit.
// Committed includes unused credit and is not measured traffic.
type Result struct {
	Committed, Usable int64
	WeekStart         time.Time
}
type balance struct {
	mu                sync.Mutex
	refs              int
	week              time.Time
	deadline          time.Time
	generation        int64
	usable, committed int64
	pending           string
	pendingWeek       time.Time
	pendingGeneration int64
	pendingSequence   int64
	epoch             atomic.Uint64
}
type Quota struct {
	pool         *pgxpool.Pool
	mu           sync.Mutex
	balances     map[string]*balance
	clock        clockState
	stop         chan struct{}
	done         chan struct{}
	beforeCommit func(context.Context, pgx.Tx) error
	afterCommit  func() error
	adjustTime   func(time.Time) time.Time
	probeQuery   func(context.Context) (time.Time, error)
	beforeLock   func()
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

// ClockHealth exposes bounded, identity-free clock status for diagnostics.
func (q *Quota) ClockHealth() (safe, warning bool, sampled time.Time) {
	safe = q.clockOK()
	q.clock.mu.Lock()
	defer q.clock.mu.Unlock()
	return safe, q.clock.warning, q.clock.sampled
}

func New(pool *pgxpool.Pool) *Quota {
	q := &Quota{pool: pool, balances: make(map[string]*balance), stop: make(chan struct{}), done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = q.Probe(ctx)
	cancel()
	go q.probeLoop()
	return q
}
func (q *Quota) Close() { close(q.stop); <-q.done }

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
func (q *Quota) acquire(id string) *balance {
	q.mu.Lock()
	defer q.mu.Unlock()
	b := q.balances[id]
	if b == nil {
		b = &balance{}
		q.balances[id] = b
	}
	b.refs++
	return b
}
func (q *Quota) release(id string, b *balance) {
	q.mu.Lock()
	defer q.mu.Unlock()
	b.refs--
	if b.refs == 0 && b.week.IsZero() && b.pending == "" {
		delete(q.balances, id)
	}
}

func (q *Quota) reapExpired() {
	q.mu.Lock()
	entries := make(map[string]*balance, len(q.balances))
	for id, b := range q.balances {
		entries[id] = b
	}
	q.mu.Unlock()
	for id, b := range entries {
		b.mu.Lock()
		if !b.deadline.IsZero() && !q.now().Before(b.deadline) {
			b.usable = 0
			b.week = time.Time{}
			b.deadline = time.Time{}
			b.committed = 0
		}
		if b.week.IsZero() && b.pending == "" {
			q.mu.Lock()
			if b.refs == 0 && q.balances[id] == b {
				delete(q.balances, id)
			}
			q.mu.Unlock()
		}
		b.mu.Unlock()
	}
}

// Invalidate discards live credit after an account policy or generation change.
// The administrator mutation path must call it after its commit.
func (q *Quota) Invalidate(id string) {
	b := q.acquire(id)
	b.epoch.Add(1)
	b.mu.Lock()
	b.usable = 0
	b.mu.Unlock()
	q.release(id, b)
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
			q.reapExpired()
		}
	}
}

// Probe compares a bounded request/response interval with the database clock.
func (q *Quota) Probe(ctx context.Context) error {
	before := q.now()
	var dbTime time.Time
	var err error
	if q.probeQuery != nil {
		dbTime, err = q.probeQuery(ctx)
	} else {
		err = q.pool.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&dbTime)
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
	q.clock.warning = offset > time.Second || offset < -time.Second || uncertainty > time.Second
	valid := uncertainty <= time.Second && offset <= 5*time.Second && offset >= -5*time.Second
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

// Take consumes confirmed credit for one account. A zero-byte call reads its
// local balance without funding. Reporters should call repeatedly for counts
// larger than the remaining local credit.
func (q *Quota) Take(ctx context.Context, id string, generation, bytes int64) (Result, error) {
	if bytes < 0 || bytes > BlockBytes {
		return Result{}, ErrTemporary
	}
	b := q.acquire(id)
	defer q.release(id, b)
	b.mu.Lock()
	defer b.mu.Unlock()
	if !q.clockOK() {
		q.clock.mu.Lock()
		shouldProbe := q.clock.sampled.IsZero() || time.Since(q.clock.sampledMono) >= 50*time.Second
		q.clock.mu.Unlock()
		if shouldProbe {
			if err := q.Probe(ctx); err != nil {
				return Result{}, ErrTemporary
			}
		}
	}
	if !q.clockOK() {
		return Result{}, ErrTemporary
	}
	now := q.now()
	if b.generation != generation {
		b.usable = 0
		b.week = time.Time{}
		b.deadline = time.Time{}
		b.committed = 0
		b.generation = generation
	} else if !b.deadline.IsZero() && !now.Before(b.deadline) {
		b.usable = 0
		b.week = time.Time{}
		b.deadline = time.Time{}
		b.committed = 0
	}
	if bytes == 0 {
		return Result{b.committed, b.usable, b.week}, nil
	}
	if bytes > b.usable {
		// A caller may not consume a partial block or pre-fund its next block.
		if b.usable > 0 {
			return Result{b.committed, b.usable, b.week}, ErrTemporary
		}
		if b.pending == "" {
			var err error
			b.pending, err = NewOperationID()
			if err != nil {
				return Result{}, ErrTemporary
			}
			b.pendingGeneration = generation
			b.pendingSequence = 0
		}
		epoch := b.epoch.Load()
		receipt, err := q.fund(ctx, id, generation, b.pending, b.pendingWeek, b.pendingGeneration, b.pendingSequence)
		if b.pendingWeek.IsZero() && !receipt.week.IsZero() {
			b.pendingWeek = receipt.week
		}
		if b.pendingSequence == 0 && receipt.sequence > 0 {
			b.pendingSequence = receipt.sequence
		}
		if err != nil {
			if errors.Is(err, ErrExhausted) || errors.Is(err, errExpiredReceipt) || errors.Is(err, errStaleOperation) {
				b.pending = ""
				b.pendingWeek = time.Time{}
				b.pendingGeneration = 0
				b.pendingSequence = 0
			}
			if errors.Is(err, errExpiredReceipt) || errors.Is(err, errStaleOperation) {
				err = ErrTemporary
			}
			return Result{b.committed, b.usable, b.week}, err
		}
		// An invalidated worker, changed generation or week may not install its receipt.
		if b.epoch.Load() != epoch || receipt.generation != generation || !q.now().Before(receipt.deadline) || !q.clockOK() {
			b.pending = ""
			b.pendingWeek = time.Time{}
			b.pendingGeneration = 0
			b.pendingSequence = 0
			return Result{}, ErrTemporary
		}
		if !b.week.Equal(receipt.week) {
			b.usable = 0
		}
		b.week = receipt.week
		b.deadline = receipt.deadline
		b.generation = generation
		b.committed = receipt.committed
		b.usable += receipt.granted
		b.pending = ""
		b.pendingWeek = time.Time{}
		b.pendingGeneration = 0
	}
	if bytes > b.usable {
		return Result{b.committed, b.usable, b.week}, ErrTemporary
	}
	b.usable -= bytes
	return Result{b.committed, b.usable, b.week}, nil
}

type funding struct {
	week, deadline                           time.Time
	generation, committed, granted, sequence int64
}

var errExpiredReceipt = errors.New("old-week receipt discarded")
var errStaleOperation = errors.New("stale quota operation")

func (q *Quota) fund(ctx context.Context, id string, generation int64, op string, pendingWeek time.Time, pendingGeneration, pendingSequence int64) (funding, error) {
	tx, err := q.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return funding{}, ErrTemporary
	}
	defer tx.Rollback(ctx)
	var status string
	var actual, cap int64
	if q.beforeLock != nil {
		q.beforeLock()
	}
	err = tx.QueryRow(ctx, `SELECT a.status,a.credential_generation,p.weekly_bytes FROM public.accounts a JOIN public.quota_plans p ON p.id=a.plan_id WHERE a.id=$1 FOR UPDATE OF a`, id).Scan(&status, &actual, &cap)
	if err != nil || status != "Active" || actual != generation {
		return funding{}, ErrTemporary
	}
	if pendingGeneration != generation {
		if pendingWeek.IsZero() {
			return funding{}, errExpiredReceipt
		}
		var oldOp string
		err = tx.QueryRow(ctx, `SELECT latest_operation_id FROM public.weekly_quota_usage WHERE account_id=$1 AND week_start=$2`, id, pendingWeek).Scan(&oldOp)
		if err != nil && err != pgx.ErrNoRows {
			return funding{week: pendingWeek}, ErrTemporary
		}
		return funding{week: pendingWeek}, errExpiredReceipt
	}
	var dbTime time.Time
	before := q.now()
	err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbTime)
	after := q.now()
	if err != nil {
		return funding{}, ErrTemporary
	}
	dbTime = q.dbTime(dbTime)
	uncertainty := after.Sub(before) / 2
	offset := dbTime.Sub(before.Add(uncertainty))
	if uncertainty > time.Second || offset > 5*time.Second || offset < -5*time.Second {
		q.clock.mu.Lock()
		q.clock.safe = false
		q.clock.recovering = 0
		q.clock.sampled = after
		q.clock.sampledMono = time.Now()
		q.clock.mu.Unlock()
		return funding{}, ErrTemporary
	}
	week := weekStart(dbTime)
	if !pendingWeek.IsZero() && !pendingWeek.Equal(week) {
		// The previous operation settles under the account lock before a new
		// week can allocate. Its receipt is never installed in the new week.
		var oldOp string
		err = tx.QueryRow(ctx, `SELECT latest_operation_id FROM public.weekly_quota_usage WHERE account_id=$1 AND week_start=$2`, id, pendingWeek).Scan(&oldOp)
		if err != nil && err != pgx.ErrNoRows {
			return funding{week: pendingWeek}, ErrTemporary
		}
		return funding{week: pendingWeek}, errExpiredReceipt
	}
	var committed, seq, granted int64
	var latest string
	err = tx.QueryRow(ctx, `SELECT committed_bytes,sequence,latest_operation_id,latest_granted_bytes FROM public.weekly_quota_usage WHERE account_id=$1 AND week_start=$2`, id, week).Scan(&committed, &seq, &latest, &granted)
	if err != nil && err != pgx.ErrNoRows {
		return funding{week: week}, ErrTemporary
	}
	if err == pgx.ErrNoRows {
		committed = 0
		seq = 0
		latest = ""
	}
	if pendingSequence == 0 {
		pendingSequence = seq + 1
	}
	if latest == op && seq != pendingSequence {
		return funding{week: week, sequence: pendingSequence}, ErrTemporary
	}
	if latest != op && seq >= pendingSequence {
		return funding{week: week, sequence: pendingSequence}, errStaleOperation
	}
	if latest != op && seq+1 != pendingSequence {
		return funding{week: week, sequence: pendingSequence}, ErrTemporary
	}
	if latest != op {
		remaining := cap - committed
		if remaining <= 0 {
			return funding{week: week, sequence: pendingSequence}, ErrExhausted
		}
		granted = BlockBytes
		if remaining < granted {
			granted = remaining
		}
		if seq == 0 {
			_, err = tx.Exec(ctx, `INSERT INTO public.weekly_quota_usage(account_id,week_start,committed_bytes,sequence,latest_operation_id,latest_granted_bytes) VALUES($1,$2,$3,1,$4,$3)`, id, week, granted, op)
		} else {
			_, err = tx.Exec(ctx, `UPDATE public.weekly_quota_usage SET committed_bytes=committed_bytes+$3,sequence=sequence+1,latest_operation_id=$4,latest_granted_bytes=$3 WHERE account_id=$1 AND week_start=$2`, id, week, granted, op)
		}
		if err != nil {
			return funding{week: week, sequence: pendingSequence}, ErrTemporary
		}
		committed += granted
	}
	if q.beforeCommit != nil {
		if err = q.beforeCommit(ctx, tx); err != nil {
			return funding{week: week, sequence: pendingSequence}, ErrTemporary
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return funding{week: week, sequence: pendingSequence}, ErrTemporary
	}
	if q.afterCommit != nil {
		if err = q.afterCommit(); err != nil {
			return funding{week: week, sequence: pendingSequence}, ErrTemporary
		}
	}
	var currentWeek time.Time
	var currentStatus string
	var currentGeneration, currentCap int64
	err = q.pool.QueryRow(ctx, `SELECT clock_timestamp(),a.status,a.credential_generation,p.weekly_bytes FROM public.accounts a JOIN public.quota_plans p ON p.id=a.plan_id WHERE a.id=$1`, id).Scan(&currentWeek, &currentStatus, &currentGeneration, &currentCap)
	currentWeek = weekStart(q.dbTime(currentWeek))
	if err != nil || !currentWeek.Equal(week) || currentStatus != "Active" || currentGeneration != generation || currentCap < committed {
		return funding{week: week, sequence: pendingSequence}, ErrTemporary
	}
	end := week.AddDate(0, 0, 7)
	until := end.Sub(dbTime) - 6*time.Second // conservative across permitted clock skew
	if until <= 0 {
		return funding{week: week}, ErrTemporary
	}
	return funding{week: week, deadline: q.now().Add(until), generation: actual, committed: committed, granted: granted, sequence: pendingSequence}, nil
}
