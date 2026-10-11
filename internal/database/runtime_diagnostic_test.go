package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"clipp-relay/internal/diagnostic"
)

func TestRuntimeWorkFailureAndRecoveryAreAggregateTransitions(t *testing.T) {
	var got []diagnostic.Event
	r := NewRuntime(nil)
	r.SetTransitions(diagnostic.NewTransitions(func(e diagnostic.Event) { got = append(got, e) }))
	secret := errors.New("SELECT private_identity FROM accounts WHERE token='SQL canary'")
	for range 2 {
		_ = r.WithUnit(context.Background(), func(context.Context) error { return secret })
	}
	_ = r.WithUnit(context.Background(), func(context.Context) error { return nil })
	_ = r.WithUnit(context.Background(), func(context.Context) error { return nil })
	if len(got) != 2 || got[0].Event != "dependency.database" || got[0].Outcome != "failed" || got[1].Outcome != "recovered" {
		t.Fatalf("database transitions = %+v", got)
	}
	if got[0].Duration < 0 || got[0].Duration > time.Second {
		t.Fatalf("bad duration: %+v", got[0])
	}
	for _, e := range got {
		if e.Reason == secret.Error() {
			t.Fatal("SQL error leaked")
		}
	}
}
