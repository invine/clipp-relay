// Package recoveryjournal owns qualification and committed-chain proof for the
// independent Recovery Journal. It performs object I/O only; callers own their
// database transactions and account mutations.
package recoveryjournal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store is the bounded object-storage surface needed to qualify and prove a
// journal snapshot. Implementations must provide conditional head replacement.
type Store interface {
	ReadHead(context.Context) (Head, string, error)
	CreateEvent(context.Context, string, []byte) error
	ReadEvent(context.Context, string) ([]byte, error)
	ReplaceHead(context.Context, string, Head) error
	ListEvents(context.Context, string) ([]string, string, error)
}

type Head struct {
	RepositoryID          string `json:"repository_id"`
	CoverageFloor         int64  `json:"coverage_floor"`
	CoverageHash          string `json:"coverage_hash"`
	Sequence              int64  `json:"sequence"`
	Hash                  string `json:"hash"`
	MaintenanceGeneration int64  `json:"maintenance_generation"`
	Format                int    `json:"format"`
}

type Event struct {
	Format       int       `json:"format"`
	AccountID    string    `json:"account_id"`
	EventID      string    `json:"event_id"`
	Sequence     int64     `json:"sequence"`
	PreviousHash string    `json:"previous_hash"`
	AdmittedAt   time.Time `json:"admitted_at"`
	Hash         string    `json:"hash"`
}

// Journal binds storage to its operator-qualified repository and coverage floor.
type Journal struct {
	store      Store
	repository string
	floor      int64
	floorHash  string
}

func New(store Store, repository string, floor int64, floorHash string) *Journal {
	return &Journal{store: store, repository: repository, floor: floor, floorHash: floorHash}
}

func (j *Journal) qualified(h Head) bool {
	if j.store == nil || j.repository == "" || j.floor < 0 || h.RepositoryID != j.repository || h.CoverageFloor != j.floor || h.CoverageHash != j.floorHash || h.Sequence < h.CoverageFloor || h.MaintenanceGeneration < 0 || h.Format != 1 {
		return false
	}
	b, e := hex.DecodeString(h.Hash)
	floor, floorErr := hex.DecodeString(h.CoverageHash)
	return e == nil && len(b) == 32 && floorErr == nil && len(floor) == 32 && (h.Sequence != h.CoverageFloor || h.Hash == h.CoverageHash)
}

// ReadHead returns a bounded, qualified immutable snapshot and its ETag.
func (j *Journal) ReadHead(ctx context.Context) (Head, string, error) {
	if j.store == nil {
		return Head{}, "", errors.New("journal unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	h, etag, err := j.store.ReadHead(bounded)
	if err != nil || !j.qualified(h) || etag == "" {
		return Head{}, "", errors.New("journal head unavailable or unqualified")
	}
	if b, _ := json.Marshal(h); len(b) > 16<<10 {
		return Head{}, "", errors.New("journal head too large")
	}
	return h, etag, nil
}

// Verify checks the committed path from the qualified floor to head. A stored
// object that is not linked to head is an orphan, not proof of deletion.
func (j *Journal) Verify(ctx context.Context, head Head, targetSeq int64, targetHash string, collect bool) ([]string, error) {
	if !j.qualified(head) {
		return nil, errors.New("journal head is unqualified")
	}
	if head.Sequence == head.CoverageFloor {
		if targetSeq != 0 {
			return nil, errors.New("journal target event not committed")
		}
		return nil, nil
	}
	expected := head.CoverageFloor + 1
	reachable := map[string]*proofNode{head.CoverageHash: {}}
	current := map[string]*proofNode{}
	cursor, lastName := "", ""
scan:
	for {
		bounded, stop := context.WithTimeout(ctx, 5*time.Second)
		names, next, err := j.store.ListEvents(bounded, cursor)
		stop()
		if err != nil || len(names) > 250 {
			return nil, errors.New("journal coverage unavailable")
		}
		for _, name := range names {
			if !strings.HasPrefix(name, "journal/events/") || name <= lastName {
				return nil, errors.New("journal event name invalid")
			}
			lastName = name
			key := strings.TrimPrefix(name, "journal/")
			var sequence int64
			if _, err := fmt.Sscanf(strings.TrimPrefix(key, "events/"), "%020d-", &sequence); err != nil {
				return nil, errors.New("journal event sequence invalid")
			}
			if sequence <= head.CoverageFloor {
				continue
			}
			if sequence > head.Sequence {
				break scan
			}
			if sequence > expected {
				if sequence != expected+1 || len(current) == 0 {
					return nil, errors.New("journal coverage gap or conflict")
				}
				reachable, current = current, map[string]*proofNode{}
				expected = sequence
			}
			if sequence != expected {
				return nil, errors.New("journal coverage gap or conflict")
			}
			bounded, stop := context.WithTimeout(ctx, 5*time.Second)
			data, readErr := j.store.ReadEvent(bounded, key)
			stop()
			if readErr != nil || len(data) > 4<<10 {
				continue
			}
			var event Event
			if json.Unmarshal(data, &event) != nil || event.Format != 1 || event.Sequence != sequence || key != fmt.Sprintf("events/%020d-%s", sequence, event.EventID) {
				continue
			}
			bare := event
			bare.Hash = ""
			encoded, _ := json.Marshal(bare)
			digest := sha256.Sum256(encoded)
			if event.Hash != hex.EncodeToString(digest[:]) {
				continue
			}
			if included, ok := reachable[event.PreviousHash]; ok {
				node := &proofNode{included: included.included || sequence == targetSeq && event.Hash == targetHash}
				if collect {
					node.account, node.parent = event.AccountID, included
				}
				current[event.Hash] = node
				if len(current) > 2048 {
					return nil, errors.New("journal event fanout exceeds bound")
				}
			}
		}
		if next == "" {
			break
		}
		if next <= cursor {
			return nil, errors.New("journal cursor did not advance")
		}
		cursor = next
	}
	included, ok := current[head.Hash]
	if !ok || expected != head.Sequence {
		return nil, errors.New("journal head hash not covered")
	}
	if targetSeq != 0 && !included.included {
		return nil, errors.New("journal target event not committed")
	}
	accounts := []string{}
	for node := included; node != nil; node = node.parent {
		if node.account != "" {
			accounts = append(accounts, node.account)
		}
	}
	return accounts, nil
}

type proofNode struct {
	account  string
	parent   *proofNode
	included bool
}
