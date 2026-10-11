package diagnostic_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/diagnostic"
)

type safeBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}
func (b *safeBuffer) String() string { b.Lock(); defer b.Unlock(); return b.Buffer.String() }

func TestLifecycleRowsAreCatalogBoundAndPrivate(t *testing.T) {
	output := &safeBuffer{}
	log := diagnostic.New(output)
	secret := "secret-account-peer-address-error-canary"
	log.Emit(diagnostic.Event{Event: "service.startup", Outcome: "success", Reason: "none", SchemaRevision: 7})
	log.Emit(diagnostic.Event{Event: "service.startup", Outcome: "failed", Reason: secret})
	log.Emit(diagnostic.Event{Event: secret, Outcome: "failed", Reason: "none"})
	log.Emit(diagnostic.Event{Event: "service.serving", Outcome: "success", Reason: "none", RequestID: secret})
	log.Close()
	rows := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(rows) != 1 {
		t.Fatalf("rows = %d: %q", len(rows), output.String())
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(rows[0]), &row); err != nil {
		t.Fatal(err)
	}
	if row["event"] != "service.startup" || row["outcome"] != "success" || row["reason"] != "none" || row["schema_revision"] != float64(7) {
		t.Fatal(row)
	}
	if strings.Contains(output.String(), secret) {
		t.Fatal("private canary escaped")
	}
}

type blockedWriter struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	dest    io.Writer
}

func (w *blockedWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.entered) })
	<-w.release
	if w.dest != nil {
		return w.dest.Write(p)
	}
	return len(p), nil
}

var _ io.Writer = (*blockedWriter)(nil)

func TestBlockedOutputDoesNotBlockProducersOrClose(t *testing.T) {
	w := &blockedWriter{entered: make(chan struct{}), release: make(chan struct{})}
	log := diagnostic.New(w)
	log.Emit(diagnostic.Event{Event: "service.startup", Outcome: "success", Reason: "none"})
	select {
	case <-w.entered:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	start := time.Now()
	for i := 0; i < 10000; i++ {
		log.Emit(diagnostic.Event{Event: "service.drain", Outcome: "started", Reason: "none"})
	}
	closeStart := time.Now()
	log.Close()
	if time.Since(closeStart) > 150*time.Millisecond {
		t.Fatal("flush exceeded 100 ms budget with scheduling tolerance")
	}
	if time.Since(start) > time.Second {
		t.Fatal("producer or close waited for blocked stdout")
	}
	close(w.release)
}

func TestDiagnosticIDsAreIndependentAndFinite(t *testing.T) {
	log := diagnostic.New(io.Discard)
	defer log.Close()
	a := log.NewID()
	if len(a) != 32 {
		t.Fatal(a)
	}
	b := log.NewID()
	if len(b) != 32 || a == b {
		t.Fatal(a, b)
	}
}

func TestActivityFloodPreservesLifecycleAndSummarizesLoss(t *testing.T) {
	output := &safeBuffer{}
	log := diagnostic.New(output)
	for i := 0; i < 100; i++ {
		log.Emit(diagnostic.Event{Event: "portal.login", Outcome: "success", Reason: "none"})
	}
	log.Emit(diagnostic.Event{Event: "service.readiness", Outcome: "recovered", Reason: "none"})
	log.Close()
	rows := output.String()
	if !strings.Contains(rows, `"event":"service.readiness"`) || !strings.Contains(rows, `"event":"log.suppressed"`) || !strings.Contains(rows, `"rate_limited_count":`) {
		t.Fatal(rows)
	}
	if strings.Count(rows, `"event":"portal.login"`) > 5 {
		t.Fatal("tuple burst exceeded")
	}
}

func TestFullQueueProducesBoundedLossSummaryAfterOutputResumes(t *testing.T) {
	// Emit through the public diagnostic seam at a rate that fills the fixed
	// queue while stdout is blocked. The normative catalog supplies independent
	// activity tuples; no private logger state is inspected.
	data, err := os.ReadFile("catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixed struct {
		Events []struct {
			Event, Log string
			Pairs      [][2]string
		}
	}
	if err := json.Unmarshal(data, &fixed); err != nil {
		t.Fatal(err)
	}
	var activity []diagnostic.Event
	for _, item := range fixed.Events {
		if item.Log == "activity" {
			activity = append(activity, diagnostic.Event{Event: item.Event, Outcome: item.Pairs[0][0], Reason: item.Pairs[0][1]})
		}
	}
	if len(activity) < 20 {
		t.Fatal("catalog lacks independent activity tuples")
	}
	output := &safeBuffer{}
	w := &blockedWriter{entered: make(chan struct{}), release: make(chan struct{}), dest: output}
	log := diagnostic.New(w)
	defer log.Close()
	for i := 0; i < 460; i++ {
		log.Emit(activity[i%len(activity)])
		time.Sleep(35 * time.Millisecond)
	}
	select {
	case <-w.entered:
	default:
		t.Fatal("writer did not block")
	}
	close(w.release)
	deadline := time.Now().Add(2 * time.Second)
	for strings.Count(output.String(), "\n") < 257 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	log.Emit(diagnostic.Event{Event: "service.readiness", Outcome: "recovered", Reason: "none"})
	log.Close()
	rows := output.String()
	foundQueueLoss := false
	var summaries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(rows), "\n") {
		if len(line) > 2048 {
			t.Fatalf("row exceeds ceiling: %d", len(line))
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if row["event"] == "log.suppressed" && row["queue_full_count"].(float64) > 0 {
			foundQueueLoss = true
		}
		if row["event"] == "log.suppressed" {
			summaries = append(summaries, row)
		}
	}
	if !foundQueueLoss {
		t.Fatalf("no positive queue-loss summary; rows=%d summaries=%v", strings.Count(rows, "\n"), summaries)
	}
}
