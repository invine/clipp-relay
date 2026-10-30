package recoveryjournal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"
)

type memoryStore struct {
	head            Head
	etag            string
	events          map[string][]byte
	failUploadOnce  bool
	failHeadOnce    bool
	advanceHeadOnce bool
}

func (m *memoryStore) ReadHead(context.Context) (Head, string, error) { return m.head, m.etag, nil }
func (m *memoryStore) CreateEvent(_ context.Context, key string, data []byte) error {
	if _, exists := m.events[key]; exists {
		return fmt.Errorf("event already exists")
	}
	if m.failUploadOnce {
		m.failUploadOnce = false
		m.events[key] = append([]byte(nil), data...)
		return fmt.Errorf("upload acknowledgement lost")
	}
	m.events[key] = append([]byte(nil), data...)
	return nil
}
func (m *memoryStore) ReadEvent(_ context.Context, key string) ([]byte, error) {
	return m.events[key], nil
}
func (m *memoryStore) ReplaceHead(_ context.Context, etag string, head Head) error {
	if etag != m.etag {
		return fmt.Errorf("conditional head replacement failed")
	}
	if m.advanceHeadOnce {
		m.advanceHeadOnce = false
		winner := makeEventForStore(m.head.Sequence+1, m.head.Hash, "other-account", "other-operation")
		winnerKey := fmt.Sprintf("events/%020d-%s", winner.Sequence, winner.EventID)
		m.events[winnerKey] = encodeEventForStore(winner)
		head.Sequence = winner.Sequence
		head.Hash = winner.Hash
		m.head = head
		m.etag = fmt.Sprintf("v%d-winner", head.Sequence)
		return fmt.Errorf("another writer advanced the head")
	}
	m.head = head
	m.etag = fmt.Sprintf("v%d", head.Sequence)
	if m.failHeadOnce {
		m.failHeadOnce = false
		return fmt.Errorf("head acknowledgement lost")
	}
	return nil
}
func (m *memoryStore) ListEvents(_ context.Context, cursor string) ([]string, string, error) {
	names := make([]string, 0, len(m.events))
	for key := range m.events {
		names = append(names, "journal/"+key)
	}
	sort.Strings(names)
	start := sort.SearchStrings(names, cursor)
	if cursor != "" {
		for start < len(names) && names[start] <= cursor {
			start++
		}
	}
	end := start + 250
	if end >= len(names) {
		return names[start:], "", nil
	}
	return names[start:end], names[end-1], nil
}

func TestProofRequiresQualifiedCommittedChainAndIgnoresOrphan(t *testing.T) {
	zero := fmt.Sprintf("%064s", "0")
	head := Head{RepositoryID: "repo", CoverageHash: zero, Hash: zero, Format: 1}
	store := &memoryStore{head: head, etag: "v1", events: map[string][]byte{}}
	journal := New(store, "repo", 0, zero)
	committed := makeEvent(t, 1, zero, "deleted-account", "a")
	orphan := makeEvent(t, 1, zero, "survivor-account", "b")
	store.events[fmt.Sprintf("events/%020d-%s", committed.Sequence, committed.EventID)] = encodeEvent(t, committed)
	store.events[fmt.Sprintf("events/%020d-%s", orphan.Sequence, orphan.EventID)] = encodeEvent(t, orphan)
	store.head.Sequence, store.head.Hash = 1, committed.Hash
	accounts, err := journal.Verify(context.Background(), store.head, 0, "", true)
	if err != nil || len(accounts) != 1 || accounts[0] != "deleted-account" {
		t.Fatalf("accounts=%v err=%v", accounts, err)
	}
	if _, _, err := New(store, "other-repo", 0, zero).ReadHead(context.Background()); err == nil {
		t.Fatal("wrong repository accepted")
	}
}

func TestProofRejectsInvalidContentsAndUncommittedTarget(t *testing.T) {
	zero := fmt.Sprintf("%064s", "0")
	event := makeEvent(t, 1, zero, "account", "a")
	store := &memoryStore{head: Head{RepositoryID: "repo", CoverageFloor: 0, CoverageHash: zero, Sequence: 1, Hash: event.Hash, Format: 1}, etag: "v1", events: map[string][]byte{}}
	key := fmt.Sprintf("events/%020d-%s", event.Sequence, event.EventID)
	store.events[key] = []byte(`{"format":1}`)
	journal := New(store, "repo", 0, zero)
	if _, err := journal.Verify(context.Background(), store.head, 0, "", true); err == nil {
		t.Fatal("invalid event contents proved head")
	}
	store.events[key] = encodeEvent(t, event)
	if _, err := journal.Verify(context.Background(), store.head, event.Sequence, "wrong-hash", false); err == nil {
		t.Fatal("uncommitted target accepted")
	}
}

func TestAppendReconcilesLostUploadAndHeadAcknowledgements(t *testing.T) {
	zero := fmt.Sprintf("%064s", "0")
	store := &memoryStore{head: Head{RepositoryID: "repo", CoverageHash: zero, Hash: zero, Format: 1}, etag: "v0", events: map[string][]byte{}, failUploadOnce: true, failHeadOnce: true}
	journal := New(store, "repo", 0, zero)
	prepareCalls := 0
	prepare := func(_ context.Context, head Head, prior *PreparedEvent) (PreparedEvent, error) {
		prepareCalls++
		if len(store.events) != 0 {
			t.Fatal("journal event was uploaded before durable preparation returned")
		}
		if prior != nil && head.Hash != prior.Event.PreviousHash {
			return makePrepared(t, head.Sequence+1, head.Hash, "account", prior.Event.EventID), nil
		}
		return makePrepared(t, head.Sequence+1, head.Hash, "account", "operation"), nil
	}
	prepared, err := journal.Append(context.Background(), nil, prepare)
	if err != nil || prepared.Event.Sequence != 1 || store.head.Hash != prepared.Event.Hash || prepareCalls != 1 {
		t.Fatalf("prepared=%+v head=%+v calls=%d err=%v", prepared, store.head, prepareCalls, err)
	}
	// The committed event is returned without preparing or uploading it again.
	if _, err := journal.Append(context.Background(), &prepared, prepare); err != nil || prepareCalls != 1 {
		t.Fatalf("committed retry changed preparation: calls=%d err=%v", prepareCalls, err)
	}
}

func TestAppendRejectsConflictingImmutableEventContent(t *testing.T) {
	zero := fmt.Sprintf("%064s", "0")
	store := &memoryStore{head: Head{RepositoryID: "repo", CoverageHash: zero, Hash: zero, Format: 1}, etag: "v0", events: map[string][]byte{}}
	prepared := makePrepared(t, 1, zero, "account", "operation")
	key := fmt.Sprintf("events/%020d-%s", prepared.Event.Sequence, prepared.Event.EventID)
	store.events[key] = []byte(`{"different":true}`)
	journal := New(store, "repo", 0, zero)
	if _, err := journal.Append(context.Background(), &prepared, func(context.Context, Head, *PreparedEvent) (PreparedEvent, error) {
		t.Fatal("available sequence must retain its immutable preparation")
		return PreparedEvent{}, nil
	}); err == nil || store.head.Sequence != 0 {
		t.Fatalf("conflicting event accepted or head advanced: head=%+v err=%v", store.head, err)
	}
}

func TestAppendRebasesOnlyAfterACommittedWinnerProvesTheOldEventIsOrphaned(t *testing.T) {
	zero := fmt.Sprintf("%064s", "0")
	store := &memoryStore{head: Head{RepositoryID: "repo", CoverageHash: zero, Hash: zero, Format: 1}, etag: "v0", events: map[string][]byte{}, advanceHeadOnce: true}
	journal := New(store, "repo", 0, zero)
	original := makePrepared(t, 1, zero, "account", "operation")
	if _, err := journal.Append(context.Background(), &original, func(context.Context, Head, *PreparedEvent) (PreparedEvent, error) {
		t.Fatal("a still-available sequence must not be rebased before proof")
		return PreparedEvent{}, nil
	}); err == nil {
		t.Fatal("lost conditional update unexpectedly succeeded")
	}
	var rebased PreparedEvent
	got, err := journal.Append(context.Background(), &original, func(_ context.Context, head Head, prior *PreparedEvent) (PreparedEvent, error) {
		if prior == nil || prior.Event.Hash != original.Event.Hash || head.Sequence != 1 || head.Hash == zero {
			t.Fatalf("rebase was not justified by the committed winner: prior=%+v head=%+v", prior, head)
		}
		rebased = makePrepared(t, head.Sequence+1, head.Hash, "account", prior.Event.EventID)
		return rebased, nil
	})
	if err != nil || got.Event.Hash != rebased.Event.Hash || store.head.Hash != rebased.Event.Hash || len(store.events) != 3 {
		t.Fatalf("got=%+v rebased=%+v head=%+v events=%d err=%v", got, rebased, store.head, len(store.events), err)
	}
}

func makePrepared(t *testing.T, seq int64, previous, account, id string) PreparedEvent {
	t.Helper()
	e := makeEvent(t, seq, previous, account, id)
	return PreparedEvent{Event: e, Data: encodeEvent(t, e)}
}

func makeEventForStore(seq int64, previous, account, id string) Event {
	e := Event{Format: 1, Sequence: seq, PreviousHash: previous, AccountID: account, EventID: id, AdmittedAt: time.Unix(seq, 0).UTC()}
	b, _ := json.Marshal(e)
	sum := sha256.Sum256(b)
	e.Hash = hex.EncodeToString(sum[:])
	return e
}

func encodeEventForStore(e Event) []byte { b, _ := json.Marshal(e); return b }

func makeEvent(t *testing.T, seq int64, previous, account, id string) Event {
	t.Helper()
	e := Event{Format: 1, Sequence: seq, PreviousHash: previous, AccountID: account, EventID: id, AdmittedAt: time.Unix(seq, 0).UTC()}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	e.Hash = hex.EncodeToString(sum[:])
	return e
}
func encodeEvent(t *testing.T, e Event) []byte {
	t.Helper()
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
