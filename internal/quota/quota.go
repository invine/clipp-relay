// Package quota records observed relay usage and owns accounting eligibility.
package quota

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"clipp-relay/internal/diagnostic"
	"clipp-relay/internal/persistence"
)

var ErrExhausted = errors.New("weekly quota exhausted")
var ErrTemporary = errors.New("quota temporarily unavailable")

type Quota struct {
	beforeLock             func()
	beforeCommit           func(context.Context, persistence.AccountingTransaction) error
	afterCommit            func() error
	periodic               periodicAccounting
	retainedUsageMu        sync.RWMutex
	retainedUsageCurrent   uint64
	retainedUsagePeppers   map[uint64][]byte
	accounting             persistence.Accounting
	diagnostics            *diagnostic.Logger
	clock                  clockState
	stop                   chan struct{}
	done                   chan struct{}
	settlementBefore       func(context.Context, persistence.AccountingTransaction) error
	settlementAfter        func(context.Context) error
	eligibilityBoundary    func(context.Context) error
	afterEligibilityRead   func(context.Context) error
	sessionReconciler      func(string, func(int) (bool, string)) error
	beforeAdmissionInstall func()
	afterAssessment        func()
	adjustTime             func(time.Time) time.Time
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

// SetEligibilityReadBoundaryForTest pauses after persistence has returned its
// authoritative eligibility result and before Quota applies that snapshot.
// Configure it before serving; tests use it to order a committed policy edit
// against a captured read result.
func (q *Quota) SetEligibilityReadBoundaryForTest(boundary func(context.Context) error) {
	q.afterEligibilityRead = boundary
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

// ConfigureRetainedUsageKeys installs the account-identity keyring used only
// for Quota's transaction-scoped retained-usage operations. Call before
// accounting starts; the secret bytes are copied into Quota's private state.
func (q *Quota) ConfigureRetainedUsageKeys(current uint64, peppers map[uint64][]byte) error {
	if current == 0 || len(peppers[current]) < 32 {
		return errors.New("current retained-usage key unavailable")
	}
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	if q.periodic.started {
		return errors.New("retained-usage keys must be configured before accounting starts")
	}
	keys := make(map[uint64][]byte, len(peppers))
	for version, material := range peppers {
		if version == 0 || len(material) < 32 {
			return errors.New("invalid retained-usage keyring")
		}
		keys[version] = append([]byte(nil), material...)
	}
	q.retainedUsageMu.Lock()
	q.retainedUsageCurrent = current
	q.retainedUsagePeppers = keys
	q.retainedUsageMu.Unlock()
	return nil
}

// RetainedUsageKey derives the privacy-minimal, keyed External Identity
// reference used by deletion, registration and recovery transactions. It
// returns no usage data and never stores the raw identity.
func (q *Quota) RetainedUsageKey(issuer, subject string) (persistence.RetainedUsageKey, error) {
	if issuer == "" || subject == "" {
		return persistence.RetainedUsageKey{}, errors.New("retained-usage identity unavailable")
	}
	q.retainedUsageMu.RLock()
	defer q.retainedUsageMu.RUnlock()
	return q.retainedUsageKeyLocked(q.retainedUsageCurrent, issuer, subject)
}

// ValidateRetainedUsageKeyCoverage fails closed when a retained record depends
// on a key Quota cannot use. These are accounting keys, independent of Auth's
// credential validation requirements.
func (q *Quota) ValidateRetainedUsageKeyCoverage(versions []uint64) error {
	q.retainedUsageMu.RLock()
	defer q.retainedUsageMu.RUnlock()
	for _, version := range versions {
		if version == 0 || len(q.retainedUsagePeppers[version]) < 32 {
			return errors.New("retained usage key unavailable")
		}
	}
	return nil
}

// RetainRecoveredUsage restores confirmed current-week usage into the retained
// identity record inside the caller's recovery transaction. The caller keeps
// ownership of account completion, journal proof and transaction lifetime.
func (q *Quota) RetainRecoveredUsage(ctx context.Context, tx persistence.RecoveryAccountingTransaction, accountID, issuer, subject string) error {
	if tx == nil || accountID == "" {
		return errors.New("recovery accounting transaction unavailable")
	}
	week, err := tx.CurrentQuotaWeek(ctx)
	if err != nil {
		return err
	}
	recorded, hasUsage, err := tx.RecordedUsage(ctx, accountID, week)
	if err != nil {
		return err
	}
	if !hasUsage {
		return nil
	}
	keys, err := q.recoveryRetainedUsageKeys(issuer, subject)
	if err != nil {
		return err
	}
	var matchingVersions []uint64
	for _, key := range keys {
		_, present, err := tx.RecoveredRetainedUsage(ctx, key.PepperVersion, key.IdentityDigest[:], week)
		if err != nil {
			return err
		}
		if present {
			matchingVersions = append(matchingVersions, key.PepperVersion)
		}
	}
	selected, err := q.selectRecoveredRetainedUsageKey(issuer, subject, matchingVersions)
	if err != nil {
		return err
	}
	return tx.StoreRecoveredRetainedUsage(ctx, selected.PepperVersion, selected.IdentityDigest[:], week, recorded)
}

// recoveryRetainedUsageKeys returns configured key candidates to detect
// previous retained records without exposing key material outside Quota.
func (q *Quota) recoveryRetainedUsageKeys(issuer, subject string) ([]persistence.RetainedUsageKey, error) {
	if issuer == "" || subject == "" {
		return nil, errors.New("retained-usage identity unavailable")
	}
	q.retainedUsageMu.RLock()
	defer q.retainedUsageMu.RUnlock()
	if _, err := q.retainedUsageKeyLocked(q.retainedUsageCurrent, issuer, subject); err != nil {
		return nil, err
	}
	versions := make([]uint64, 0, len(q.retainedUsagePeppers))
	for version := range q.retainedUsagePeppers {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	keys := make([]persistence.RetainedUsageKey, 0, len(versions))
	for _, version := range versions {
		key, err := q.retainedUsageKeyLocked(version, issuer, subject)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// selectRecoveredRetainedUsageKey chooses the existing matching key for a
// restored identity or the current key when no record exists. Multiple matches
// identify duplicate logical retained records and must abort the recovery.
func (q *Quota) selectRecoveredRetainedUsageKey(issuer, subject string, matchingVersions []uint64) (persistence.RetainedUsageKey, error) {
	current, err := q.RetainedUsageKey(issuer, subject)
	if err != nil {
		return persistence.RetainedUsageKey{}, err
	}
	if len(matchingVersions) > 1 {
		return persistence.RetainedUsageKey{}, errors.New("duplicate logical retained usage")
	}
	if len(matchingVersions) == 0 {
		return current, nil
	}
	q.retainedUsageMu.RLock()
	defer q.retainedUsageMu.RUnlock()
	return q.retainedUsageKeyLocked(matchingVersions[0], issuer, subject)
}

func (q *Quota) retainedUsageKeyLocked(version uint64, issuer, subject string) (persistence.RetainedUsageKey, error) {
	key := q.retainedUsagePeppers[version]
	if version == 0 || len(key) < 32 {
		return persistence.RetainedUsageKey{}, errors.New("current retained-usage key unavailable")
	}
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte("clipp-relay/v1/retained-quota\x00" + issuer + "\x00" + subject))
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return persistence.RetainedUsageKey{PepperVersion: version, IdentityDigest: digest}, nil
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

// newOperationID produces a cryptographically random stable operation identifier.
func newOperationID() (string, error) {
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
