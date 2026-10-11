package main

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRejectedStartupDoesNotEchoConfigurationCanary(t *testing.T) {
	const canary = "secret-identity-address-error-canary"
	if os.Getenv("CLIPP_STARTUP_HELPER") == "1" {
		flag.CommandLine = flag.NewFlagSet("relay", flag.ContinueOnError)
		os.Args = []string{"clipp-relay", "-command=serve", "-config=/tmp/" + canary}
		os.Exit(run())
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRejectedStartupDoesNotEchoConfigurationCanary")
	cmd.Env = append(os.Environ(), "CLIPP_STARTUP_HELPER=1")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("invalid configuration accepted")
	}
	if strings.Contains(string(output), canary) || !strings.Contains(string(output), `"event":"service.startup"`) || !strings.Contains(string(output), `"reason":"configuration_rejected"`) {
		t.Fatal(string(output))
	}
}

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

func TestDrainResultSeparatesCompletionDeadlineAndForce(t *testing.T) {
	cases := []struct {
		err             error
		outcome, reason string
	}{
		{nil, "success", "none"},
		{context.DeadlineExceeded, "failed", "deadline"},
		{context.Canceled, "failed", "forced"},
	}
	for _, c := range cases {
		e := drainResult(c.err, 25*time.Millisecond)
		if e.Event != "service.drain" || e.Outcome != c.outcome || e.Reason != c.reason || e.Duration != 25*time.Millisecond {
			t.Fatal(e)
		}
	}
}
