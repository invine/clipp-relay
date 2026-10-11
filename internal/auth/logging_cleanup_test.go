package auth_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/diagnostic"
)

type cleanupDiagnosticOutput struct {
	mu   sync.Mutex
	rows []byte
}

func (o *cleanupDiagnosticOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.rows = append(o.rows, p...)
	return len(p), nil
}

func (o *cleanupDiagnosticOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.rows)
}

func TestCleanupLogsFailedPassWithoutRoutineSuccessRows(t *testing.T) {
	c, m, db := fixture(t)
	s := auth.NewWithRuntime(db, c, m, auth.Provider{})
	var healthyOutput cleanupDiagnosticOutput
	healthyLog := diagnostic.New(&healthyOutput)
	s.SetDiagnostics(healthyLog)
	healthyCtx, stopHealthy := context.WithCancel(context.Background())
	healthyDone := make(chan struct{})
	go func() { s.Maintain(healthyCtx); close(healthyDone) }()
	deadline := time.Now().Add(10 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		sample, err := s.SampleCleanupSignals(context.Background())
		if err != nil {
			stopHealthy()
			<-healthyDone
			t.Fatal(err)
		}
		if sample.Completed > 0 {
			completed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopHealthy()
	<-healthyDone
	if !completed {
		t.Fatal("healthy cleanup did not complete a pass")
	}
	healthyLog.Close()
	if strings.Contains(healthyOutput.String(), `"event":"maintenance.cleanup"`) {
		t.Fatalf("routine cleanup produced periodic rows: %s", healthyOutput.String())
	}

	// A closed disposable pool makes the next actual worker pass fail without
	// logging a raw SQL or connection error.
	db.Pool.Close()
	var failedOutput cleanupDiagnosticOutput
	failedLog := diagnostic.New(&failedOutput)
	s.SetDiagnostics(failedLog)
	failedCtx, stopFailed := context.WithCancel(context.Background())
	failedDone := make(chan struct{})
	go func() { s.Maintain(failedCtx); close(failedDone) }()
	deadline = time.Now().Add(5 * time.Second)
	for !strings.Contains(failedOutput.String(), `"event":"maintenance.cleanup"`) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stopFailed()
	<-failedDone
	failedLog.Close()
	rows := actionRows(t, failedOutput.String())
	requireActionRow(t, rows, "maintenance.cleanup", "failed", "database_unavailable")
	if strings.Contains(failedOutput.String(), c.Database.Name) || strings.Contains(failedOutput.String(), "postgres://") {
		t.Fatalf("database detail leaked: %s", failedOutput.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(failedOutput.String()), "\n") {
		if strings.Contains(line, `"event":"maintenance.cleanup"`) {
			t.Logf("captured cleanup: %s", line)
			break
		}
	}
}
