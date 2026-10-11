package diagnostic

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrInvalidResponse is a safe classification marker. Upstream payloads stay
// in the caller's error path and never enter diagnostic events.
var ErrInvalidResponse = errors.New("dependency response invalid")

// Transitions retains only fixed, process-local dependency states. It never
// records an upstream error, identifier, URL, or response body.
type Transitions struct {
	mu     sync.Mutex
	emit   func(Event)
	failed map[string]bool
}

func NewTransitions(emit func(Event)) *Transitions {
	return &Transitions{emit: emit, failed: make(map[string]bool, 5)}
}

func (t *Transitions) Dependency(name string, err error, ctx context.Context, elapsed time.Duration) {
	if t == nil || t.emit == nil {
		return
	}
	// Reject caller-selected names rather than growing a state map.
	switch name {
	case "dependency.database", "dependency.google_token", "dependency.google_jwks", "dependency.publication_api", "dependency.deletion_journal":
	default:
		return
	}
	t.mu.Lock()
	wasFailed := t.failed[name]
	failed := err != nil
	if wasFailed == failed {
		t.mu.Unlock()
		return
	}
	t.failed[name] = failed
	e := Event{Event: name, Outcome: "recovered", Reason: "none", Duration: elapsed}
	if failed {
		e.Outcome, e.Reason = "failed", "unavailable"
		if errors.Is(ctx.Err(), context.Canceled) {
			e.Reason = "canceled"
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			e.Reason = "deadline"
		} else if errors.Is(err, ErrInvalidResponse) {
			e.Reason = "invalid_response"
		}
	}
	t.emit(e)
	t.mu.Unlock()
}
