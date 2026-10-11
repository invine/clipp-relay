package diagnostic_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/diagnostic"
)

func TestMaintenanceObservationTransitionsAndFreshness(t *testing.T) {
	var got []diagnostic.Event
	now := time.Now()
	failed, level := false, "none"
	o := diagnostic.NewObservations(func(e diagnostic.Event) { got = append(got, e) }, func(context.Context) (string, error) {
		if failed {
			return "", errors.New("SQL canary")
		}
		return level, nil
	}, func(context.Context) (string, error) { return "none", nil })
	o.Tick(context.Background(), now)
	o.Wait()
	unavailable := map[string]bool{}
	for _, e := range got {
		if e.Event == "observation.state" && e.State == "unavailable" {
			unavailable[e.Signal] = true
		}
	}
	for _, signal := range []string{"recovery", "full_backup", "repository_retention", "certificate", "database_resources"} {
		if !unavailable[signal] {
			t.Fatalf("absent producer %s not unavailable: %+v", signal, got)
		}
	}
	level = "warning"
	o.Tick(context.Background(), now.Add(time.Minute))
	o.Wait()
	failed = true
	o.Tick(context.Background(), now.Add(2*time.Minute))
	o.Wait()
	failed = false
	level = "critical"
	o.Tick(context.Background(), now.Add(3*time.Minute))
	o.Wait()
	o.Tick(context.Background(), now.Add(6*time.Minute+time.Second))
	o.Wait()
	// A missing fresh observation expires before the next successful pass.
	var cleanup []diagnostic.Event
	for _, e := range got {
		if e.Signal == "cleanup" && !(e.State == "unknown") {
			cleanup = append(cleanup, e)
		}
	}
	if len(cleanup) < 6 || cleanup[len(cleanup)-2].State != "stale" {
		t.Fatalf("missing stale transition = %+v", cleanup)
	}
	level = "none"
	o.Tick(context.Background(), now.Add(7*time.Minute))
	o.Wait()
	// A fresh lower sample clears the retained threshold; failure and staleness
	// alone never claim that the backlog has cleared.
	cleanup = cleanup[:0]
	for _, e := range got {
		if e.Signal == "cleanup" && e.State != "unknown" {
			cleanup = append(cleanup, e)
		}
	}
	want := []string{"available", "warning", "failed", "available", "critical", "stale", "available", "none"}
	for i, v := range want {
		if i >= len(cleanup) {
			t.Fatalf("missing transition %s in %+v", v, cleanup)
		}
		if cleanup[i].State != v && cleanup[i].Reason != v {
			t.Fatalf("transition %d = %+v, want %s", i, cleanup[i], v)
		}
	}
	if cleanup[len(cleanup)-1].Event != "observation.threshold" || cleanup[len(cleanup)-1].Outcome != "recovered" {
		t.Fatalf("threshold clearance = %+v", cleanup[len(cleanup)-1])
	}
	for _, e := range got {
		if e.Reason == "SQL canary" {
			t.Fatal("raw error leaked")
		}
	}
}

func TestMaintenanceObservationRowsStayCatalogBounded(t *testing.T) {
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	o := diagnostic.NewObservations(logger.Emit, func(context.Context) (string, error) { return "", errors.New("SQL canary") }, func(context.Context) (string, error) { return "none", nil })
	o.Tick(context.Background(), time.Now())
	o.Wait()
	logger.Close()
	rows := strings.TrimSpace(output.String())
	if rows == "" || strings.Contains(rows, "SQL canary") {
		t.Fatalf("missing or unsafe rows: %s", rows)
	}
	for _, line := range strings.Split(rows, "\n") {
		if len(line) > 2048 {
			t.Fatalf("oversized observation row: %d", len(line))
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if len(row) > 8 {
			t.Fatalf("unbounded observation fields: %v", row)
		}
		if row["event"] == "observation.state" && row["state"] == "unavailable" {
			if _, ok := row["value"]; ok {
				t.Fatalf("unavailable value invented: %v", row)
			}
		}
	}
}

func TestUnsupportedProcessObservationsEmitUnavailableRowsOnce(t *testing.T) {
	var output bytes.Buffer
	logger := diagnostic.New(&output)
	o := diagnostic.NewObservations(logger.Emit, nil, nil)
	o.Tick(context.Background(), time.Now())
	o.Wait()
	o.Tick(context.Background(), time.Now().Add(time.Minute))
	o.Wait()
	logger.Close()
	seen := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["event"] != "observation.state" {
			continue
		}
		signal, _ := row["signal"].(string)
		if signal != "process_cpu" && signal != "process_memory" {
			continue
		}
		if row["state"] != "unavailable" || row["outcome"] != "observed" || row["reason"] != "producer_unavailable" || row["level"] != "INFO" {
			t.Fatalf("unsupported process signal row: %v", row)
		}
		if _, ok := row["value"]; ok {
			t.Fatalf("unsupported process signal has value: %v", row)
		}
		seen[signal]++
	}
	if seen["process_cpu"] != 1 || seen["process_memory"] != 1 {
		t.Fatalf("process signal rows = %v; output = %s", seen, output.String())
	}
}

func TestQuotaClockObservationDoesNotPromoteOldSafetyProbe(t *testing.T) {
	var got []diagnostic.Event
	o := diagnostic.NewObservations(func(e diagnostic.Event) {
		if e.Signal == "quota_clock" {
			got = append(got, e)
		}
	}, nil, nil)
	now := time.Now()
	o.Clock(true, now, now)
	o.Clock(true, now, now.Add(time.Minute))
	o.Clock(false, now.Add(2*time.Minute), now.Add(2*time.Minute))
	o.Clock(false, now.Add(2*time.Minute), now.Add(2*time.Minute))
	o.Clock(true, now.Add(3*time.Minute), now.Add(3*time.Minute))
	o.Clock(true, now.Add(3*time.Minute), now.Add(6*time.Minute))
	want := []string{"unknown", "available", "failed", "available", "stale"}
	if len(got) != len(want) {
		t.Fatalf("clock transitions = %+v", got)
	}
	for i, state := range want {
		if got[i].State != state {
			t.Fatalf("clock transition %d = %+v", i, got[i])
		}
	}
}

func TestMaintenanceObservationHasOneInFlightPass(t *testing.T) {
	entered := make(chan struct{}, 2)
	deadlines := make(chan time.Duration, 2)
	release := make(chan struct{})
	o := diagnostic.NewObservations(func(diagnostic.Event) {}, func(ctx context.Context) (string, error) {
		entered <- struct{}{}
		deadline, ok := ctx.Deadline()
		if ok {
			deadlines <- time.Until(deadline)
		}
		select {
		case <-release:
			return "none", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}, nil)
	now := time.Now()
	o.Tick(context.Background(), now)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("pass did not start")
	}
	select {
	case remaining := <-deadlines:
		if remaining < 4*time.Second || remaining > 5*time.Second {
			t.Fatalf("pass deadline %s", remaining)
		}
	default:
		t.Fatal("pass lacks deadline")
	}
	o.Tick(context.Background(), now.Add(time.Minute))
	select {
	case <-entered:
		t.Fatal("second pass started while first was in flight")
	default:
	}
	close(release)
	o.Wait()
	o.Tick(context.Background(), now.Add(2*time.Minute))
	o.Wait()
	select {
	case <-entered:
	default:
		t.Fatal("next pass did not start after release")
	}
}
