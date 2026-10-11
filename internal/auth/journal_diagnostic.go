package auth

import (
	"context"
	"time"

	"clipp-relay/internal/diagnostic"
)

// observedJournal preserves the storage contract and emits only finite
// dependency transitions. No object key or body enters diagnostic state.
type observedJournal struct {
	DeletionJournal
	transitions *diagnostic.Transitions
}

func (j observedJournal) report(ctx context.Context, started time.Time, err error) {
	j.transitions.Dependency("dependency.deletion_journal", err, ctx, time.Since(started))
}
func (j observedJournal) ReadHead(ctx context.Context) (JournalHead, string, error) {
	// The caller must qualify the head against the configured repository and
	// coverage floor before a successful read can prove recovery.
	return j.DeletionJournal.ReadHead(ctx)
}
func (j observedJournal) CreateEvent(ctx context.Context, key string, data []byte) error {
	started := time.Now()
	err := j.DeletionJournal.CreateEvent(ctx, key, data)
	j.report(ctx, started, err)
	return err
}
func (j observedJournal) ReadEvent(ctx context.Context, key string) ([]byte, error) {
	started := time.Now()
	data, err := j.DeletionJournal.ReadEvent(ctx, key)
	j.report(ctx, started, err)
	return data, err
}
func (j observedJournal) ReplaceHead(ctx context.Context, etag string, head JournalHead) error {
	started := time.Now()
	err := j.DeletionJournal.ReplaceHead(ctx, etag, head)
	j.report(ctx, started, err)
	return err
}
func (j observedJournal) ListEvents(ctx context.Context, cursor string) ([]string, string, error) {
	started := time.Now()
	names, next, err := j.DeletionJournal.ListEvents(ctx, cursor)
	j.report(ctx, started, err)
	return names, next, err
}
