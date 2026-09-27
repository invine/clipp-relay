package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestFirstSignalStartsDrainAndSecondForcesIt(t *testing.T) {
	signals := make(chan os.Signal, 2)
	stopping, stop := context.WithCancel(context.Background())
	defer stop()
	forcing, force := context.WithCancel(context.Background())
	defer force()
	go handleSignals(signals, stop, force)
	signals <- syscall.SIGTERM
	select {
	case <-stopping.Done():
	case <-time.After(time.Second):
		t.Fatal("first signal did not start drain")
	}
	select {
	case <-forcing.Done():
		t.Fatal("first signal forced drain")
	default:
	}
	signals <- syscall.SIGINT
	select {
	case <-forcing.Done():
	case <-time.After(time.Second):
		t.Fatal("second signal did not force drain")
	}
}

func TestDrainDeadlineForcesWithoutSecondSignal(t *testing.T) {
	signals := make(chan os.Signal, 2)
	stopping, stop := context.WithCancel(context.Background())
	defer stop()
	forcing, force := context.WithCancel(context.Background())
	defer force()
	go handleSignals(signals, stop, force)
	signals <- syscall.SIGTERM
	<-stopping.Done()
	done := make(chan error, 1)
	go func() {
		done <- drainWithDeadline(forcing, 20*time.Millisecond, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	}()
	select {
	case err := <-done:
		if err != context.DeadlineExceeded {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("drain deadline did not force")
	}
}
