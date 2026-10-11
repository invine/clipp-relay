package diagnostic

import (
	"context"
	"sync"
	"time"
)

// LevelSampler returns the highest currently crossed aggregate threshold.
// Only these four fixed values are accepted; no numeric or identity data is logged.
type LevelSampler func(context.Context) (string, error)

type observation struct {
	state, level string
	lastSuccess  time.Time
}

// Observations is a bounded, non-authoritative diagnostic sampler. Existing
// maintenance workers and quota-clock safety probes keep their own cadence.
type Observations struct {
	mu                sync.Mutex
	emit              func(Event)
	cleanup, deletion LevelSampler
	items             map[string]observation
	inFlight          bool
	done              chan struct{}
}

func NewObservations(emit func(Event), cleanup, deletion LevelSampler) *Observations {
	return &Observations{emit: emit, cleanup: cleanup, deletion: deletion, items: make(map[string]observation, 10)}
}

func (o *Observations) state(signal, state, outcome, reason string) {
	o.emit(Event{Event: "observation.state", Signal: signal, State: state, Outcome: outcome, Reason: reason})
}

func (o *Observations) initialLocked() {
	for _, signal := range []string{"recovery", "full_backup", "repository_retention", "certificate", "database_resources"} {
		if _, ok := o.items[signal]; !ok {
			o.items[signal] = observation{state: "unavailable"}
			o.state(signal, "unavailable", "observed", "producer_unavailable")
		}
	}
	for _, signal := range []string{"cleanup", "pending_deletion", "quota_clock"} {
		if _, ok := o.items[signal]; !ok {
			o.items[signal] = observation{state: "unknown"}
			o.state(signal, "unknown", "observed", "observation_unknown")
		}
	}
}

// Tick starts at most one five-second pass. It checks freshness synchronously
// even if a prior pass has not returned, so blocked producers cannot extend it.
func (o *Observations) Tick(parent context.Context, now time.Time) {
	if o == nil || o.emit == nil {
		return
	}
	o.mu.Lock()
	o.initialLocked()
	for _, signal := range []string{"cleanup", "pending_deletion", "quota_clock"} {
		item := o.items[signal]
		if !item.lastSuccess.IsZero() && now.Sub(item.lastSuccess) >= 180*time.Second && item.state != "stale" {
			item.state = "stale"
			o.items[signal] = item
			o.state(signal, "stale", "failed", "observation_stale")
		}
	}
	if o.inFlight {
		o.mu.Unlock()
		return
	}
	o.inFlight = true
	o.done = make(chan struct{})
	done := o.done
	o.mu.Unlock()
	go func() {
		defer func() { o.mu.Lock(); o.inFlight = false; close(done); o.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()
		for _, source := range []struct {
			signal string
			sample LevelSampler
		}{{"cleanup", o.cleanup}, {"pending_deletion", o.deletion}} {
			if source.sample == nil {
				continue
			}
			level, err := source.sample(ctx)
			o.record(source.signal, level, err, now)
		}
	}()
}

// Clock records the existing quota safety probe's result without running a
// second probe or lengthening its stricter authority window.
func (o *Observations) Clock(safe bool, sampled, now time.Time) {
	if o == nil || o.emit == nil {
		return
	}
	o.mu.Lock()
	o.initialLocked()
	item := o.items["quota_clock"]
	if sampled.IsZero() {
		o.mu.Unlock()
		return
	}
	if now.Sub(sampled) >= 180*time.Second {
		if item.state != "stale" {
			item.state = "stale"
			o.state("quota_clock", "stale", "failed", "observation_stale")
		}
		o.items["quota_clock"] = item
		o.mu.Unlock()
		return
	}
	o.mu.Unlock()
	if safe {
		o.record("quota_clock", "none", nil, sampled)
	} else {
		o.record("quota_clock", "", context.DeadlineExceeded, now)
	}
}

func (o *Observations) record(signal, level string, err error, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	item := o.items[signal]
	if err != nil || (level != "none" && level != "warning" && level != "critical" && level != "breach") {
		if item.state != "failed" {
			item.state = "failed"
			o.state(signal, "failed", "failed", "observation_failed")
		}
		o.items[signal] = item
		return
	}
	if item.state != "available" {
		outcome := "observed"
		if item.state == "failed" || item.state == "stale" {
			outcome = "recovered"
		}
		o.state(signal, "available", outcome, "none")
	}
	if level != item.level {
		outcome, reason := "observed", level
		if level == "none" && item.level != "" {
			outcome = "recovered"
		}
		if level != "none" || item.level != "" {
			o.emit(Event{Event: "observation.threshold", Signal: signal, Outcome: outcome, Reason: reason})
		}
	}
	item.state, item.level, item.lastSuccess = "available", level, now
	o.items[signal] = item
}

// Wait waits for the current bounded pass; intended for deterministic callers.
func (o *Observations) Wait() {
	o.mu.Lock()
	done := o.done
	o.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (o *Observations) Run(ctx context.Context) {
	o.Tick(ctx, time.Now())
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			o.Tick(ctx, now)
		}
	}
}
