package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/config"
	"clipp-relay/internal/diagnostic"
)

type failingHead struct {
	auth.DeletionJournal
	fail bool
	bad  bool
}

func (j *failingHead) ReadHead(context.Context) (auth.JournalHead, string, error) {
	if j.fail {
		return auth.JournalHead{}, "", errors.New("OCI secret canary")
	}
	zero := strings.Repeat("0", 64)
	repository := "test-repository"
	if j.bad {
		repository = "wrong-repository"
	}
	return auth.JournalHead{RepositoryID: repository, CoverageFloor: 0, CoverageHash: zero, Sequence: 0, Hash: zero, Format: 1}, "etag", nil
}

func TestQualifiedJournalFailureAndRecoveryAreSafeTransitions(t *testing.T) {
	var got []diagnostic.Event
	s := auth.New(nil, config.Config{}, config.Material{}, auth.Provider{})
	s.SetTransitions(diagnostic.NewTransitions(func(e diagnostic.Event) { got = append(got, e) }))
	j := &failingHead{fail: true}
	s.SetDeletionJournal(j, "test-repository", 0, strings.Repeat("0", 64))
	for range 2 {
		if err := s.ValidateDeletionJournal(context.Background()); err == nil {
			t.Fatal("bad journal accepted")
		}
	}
	j.fail = false
	j.bad = true
	if err := s.ValidateDeletionJournal(context.Background()); err == nil {
		t.Fatal("unqualified head accepted")
	}
	j.bad = false
	for range 2 {
		if err := s.ValidateDeletionJournal(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0].Event != "dependency.deletion_journal" || got[0].Outcome != "failed" || got[1].Outcome != "recovered" {
		t.Fatalf("journal transitions = %+v", got)
	}
	for _, e := range got {
		if e.Reason == "OCI secret canary" {
			t.Fatal("upstream error leaked")
		}
	}
}
