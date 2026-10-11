package diagnostic_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"clipp-relay/internal/diagnostic"
)

func TestDependencyTransitionsEmitFailureAndProvenRecoveryOnly(t *testing.T) {
	var got []diagnostic.Event
	tracker := diagnostic.NewTransitions(func(e diagnostic.Event) { got = append(got, e) })
	tracker.Dependency("dependency.database", nil, context.Background(), time.Millisecond)
	tracker.Dependency("dependency.database", errors.New("SQL secret canary"), context.Background(), time.Millisecond)
	tracker.Dependency("dependency.database", errors.New("another failure"), context.Background(), time.Millisecond)
	tracker.Dependency("dependency.database", nil, context.Background(), time.Millisecond)
	tracker.Dependency("dependency.database", nil, context.Background(), time.Millisecond)
	if len(got) != 2 || got[0].Outcome != "failed" || got[0].Reason != "unavailable" || got[1].Outcome != "recovered" {
		t.Fatalf("transitions = %+v", got)
	}
	for _, event := range got {
		if event.Reason == "SQL secret canary" {
			t.Fatal("raw dependency error leaked")
		}
	}
}

func TestDependencyInvalidResponseUsesSafeCategory(t *testing.T) {
	var got []diagnostic.Event
	transitions := diagnostic.NewTransitions(func(e diagnostic.Event) { got = append(got, e) })
	transitions.Dependency("dependency.google_jwks", diagnostic.ErrInvalidResponse, context.Background(), time.Millisecond)
	if len(got) != 1 || got[0].Reason != "invalid_response" {
		t.Fatalf("category = %+v", got)
	}
}
