package postgres_test

import (
	"context"
	"errors"
	"testing"

	"clipp-relay/internal/database"
	"clipp-relay/internal/persistence"
	"clipp-relay/internal/persistence/postgres"
)

func TestOperationsShareAdmissionAndReleaseCapacity(t *testing.T) {
	work := postgres.New(database.NewRuntime(nil))
	operations := make([]persistence.Operation, 64)
	for i := range operations {
		op, err := work.Start(context.Background())
		if err != nil {
			t.Fatalf("start operation %d: %v", i, err)
		}
		operations[i] = op
	}
	if _, err := work.Start(context.Background()); !errors.Is(err, persistence.ErrUnavailable) || !errors.Is(err, persistence.ErrWorkUnavailable) {
		t.Fatalf("overflow start error = %v, want classified work unavailability", err)
	}
	operations[0].Close()
	op, err := work.Start(context.Background())
	if err != nil {
		t.Fatalf("operation did not release shared capacity: %v", err)
	}
	op.Close()
	for _, operation := range operations {
		operation.Close()
	}
}

func TestOperationPreservesParentCancellationAsTemporaryFailure(t *testing.T) {
	work := postgres.New(database.NewRuntime(nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := work.Start(ctx); !errors.Is(err, persistence.ErrUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled operation result = %v, want classified unavailability and cancellation", err)
	}
}
