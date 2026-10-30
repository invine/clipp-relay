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
	head   Head
	etag   string
	events map[string][]byte
}

func (m *memoryStore) ReadHead(context.Context) (Head, string, error)  { return m.head, m.etag, nil }
func (*memoryStore) CreateEvent(context.Context, string, []byte) error { return nil }
func (m *memoryStore) ReadEvent(_ context.Context, key string) ([]byte, error) {
	return m.events[key], nil
}
func (*memoryStore) ReplaceHead(context.Context, string, Head) error { return nil }
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
