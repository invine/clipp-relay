package quota

import (
	"context"
	"errors"
	"time"
)

var ErrSessionLimit = errors.New("relay session limit reached")

const maxKnownZeroLiveCounts = 6000

type SessionChangeKind uint8

const (
	SessionNew SessionChangeKind = iota + 1
	SessionRenewal
	SessionReplacement
)

// SessionChange describes how a successful registry installation affects
// account occupancy. Relay owns the connection and supplies the exact affected
// account counts from its registry lock.
type SessionChange struct {
	Kind            SessionChangeKind
	PreviousAccount string
}

type assessmentState struct {
	account         string
	accountIdentity uint64
	revision        uint64
	week            time.Time
	capBytes        int64
	sessionLimit    int
	used            bool
}

// Assessment is an opaque, one-use decision context from Quota. Callers cannot
// construct or inspect its policy and accounting provenance.
type Assessment struct{ state *assessmentState }

// Assess reads the current durable policy and usage, then binds the result to
// the current in-memory accounting state identity and revision.
func (q *Quota) Assess(ctx context.Context, account string) (Assessment, error) {
	eligibility, err := q.Eligibility(ctx, account)
	if err != nil {
		return Assessment{}, err
	}
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	a := q.periodic.accounts[account]
	if a == nil || a.revision != eligibility.Revision || !a.eligibleWeek.Equal(eligibility.WeekStart) {
		return Assessment{}, ErrTemporary
	}
	return Assessment{state: &assessmentState{
		account:         account,
		accountIdentity: a.identity,
		revision:        a.revision,
		week:            eligibility.WeekStart,
		capBytes:        eligibility.CapBytes,
		sessionLimit:    eligibility.SessionLimit,
	}}, nil
}

// CommitAdmission serializes the final quota decision, Relay's in-memory
// installation, and publication of the resulting absolute live counts. Relay
// must hold its registry lock for the duration. install must perform only
// in-memory registry work and must not call back into Quota.
func (q *Quota) CommitAdmission(assessment Assessment, change SessionChange, install func() map[string]int) error {
	state := assessment.state
	if state == nil || install == nil || change.Kind < SessionNew || change.Kind > SessionReplacement {
		return ErrTemporary
	}
	if !q.clockOK() {
		return ErrTemporary
	}
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	a := q.periodic.accounts[state.account]
	currentWeek := weekStart(q.now())
	if state.used || a == nil || a.identity != state.accountIdentity || a.revision != state.revision ||
		!a.eligibleWeek.Equal(state.week) || !state.week.Equal(currentWeek) || a.blocked || q.accountingExpiredLocked(a) {
		return ErrTemporary
	}
	week := a.weeks[state.week]
	effective, valid := week.effectiveBytes()
	if !valid {
		return ErrTemporary
	}
	if effective >= state.capBytes {
		return ErrExhausted
	}
	occupancy := q.periodic.liveCounts[state.account]
	switch change.Kind {
	case SessionNew:
		occupancy++
	case SessionRenewal:
		if occupancy == 0 {
			return ErrTemporary
		}
	case SessionReplacement:
		if change.PreviousAccount == "" || q.periodic.liveCounts[change.PreviousAccount] == 0 {
			return ErrTemporary
		}
		if change.PreviousAccount != state.account {
			occupancy++
		}
	}
	if change.Kind != SessionRenewal && occupancy > state.sessionLimit {
		return ErrSessionLimit
	}

	state.used = true
	counts := install()
	q.publishLiveCountsLocked(counts)
	return nil
}

// PublishLiveCounts atomically applies final absolute counts reported by Relay
// for sessions that ended or were retired. Zero counts remove idle count state.
func (q *Quota) PublishLiveCounts(counts map[string]int) {
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	q.publishLiveCountsLocked(counts)
}

func (q *Quota) publishLiveCountsLocked(counts map[string]int) {
	if q.periodic.liveCounts == nil {
		q.periodic.liveCounts = make(map[string]int)
	}
	if q.periodic.liveZeroOrder == nil {
		q.periodic.liveZeroOrder = make(map[string]uint64)
	}
	for account, count := range counts {
		if count < 0 {
			continue
		}
		if count > 0 {
			q.periodic.liveCounts[account] = count
			delete(q.periodic.liveZeroOrder, account)
		} else {
			q.periodic.liveCounts[account] = 0
			q.periodic.liveSequence++
			q.periodic.liveZeroOrder[account] = q.periodic.liveSequence
		}
	}
	for len(q.periodic.liveZeroOrder) > maxKnownZeroLiveCounts {
		var oldestAccount string
		var oldestSequence uint64
		for account, sequence := range q.periodic.liveZeroOrder {
			if oldestAccount == "" || sequence < oldestSequence {
				oldestAccount, oldestSequence = account, sequence
			}
		}
		delete(q.periodic.liveZeroOrder, oldestAccount)
		delete(q.periodic.liveCounts, oldestAccount)
	}
}

// LiveSessionCount reports whether Quota has a recent process-local count for
// the account. Known-zero samples are retained in a bounded recent set.
func (q *Quota) LiveSessionCount(account string) (int, bool) {
	q.periodic.mu.Lock()
	defer q.periodic.mu.Unlock()
	count, ok := q.periodic.liveCounts[account]
	return count, ok
}
