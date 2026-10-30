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

func TestValidateConfigChecksStaticServingConfigurationWithoutOpeningDependencies(t *testing.T) {
	if os.Getenv("CLIPP_VALIDATE_HELPER") == "1" {
		flag.CommandLine = flag.NewFlagSet("relay", flag.ContinueOnError)
		os.Args = []string{"clipp-relay", "-command=validate-config", "-config=" + os.Getenv("CLIPP_VALIDATE_CONFIG")}
		os.Exit(run())
	}
	base := `{"version":1,"portal_origin":"https://portal.example.test","wss_hostname":"relay.example.test","public_clients":{"android_redirect":"clipp-relay://oauth/callback","extension_redirect":"https://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.chromiumapp.org/clipp-relay"},"listeners":{"public":"127.0.0.1:18080","private":"127.0.0.1:18081"},"relay_tcp":{"listen":"/ip4/127.0.0.1/tcp/18082","public_addresses":["/ip4/192.0.2.10/tcp/18082"]},"database":{"mode":"external","host":"db.example.test","port":5432,"name":"relay","username_file":"/missing/db-user","password_file":"/missing/db-password","ca_file":"/missing/db-ca"},"journal":{"region":"us-ashburn-1","namespace":"ns","bucket":"bucket","repository_id":"repo","coverage_floor":0,"coverage_hash":"` + strings.Repeat("0", 64) + `","tenancy_ocid":"ocid1.tenancy.oc1..example","user_ocid":"ocid1.user.oc1..example","fingerprint":"aa:bb","private_key_file":"/missing/journal-key"},"secrets":{"google_client_id_file":"/missing/google-id","google_client_secret_file":"/missing/google-secret","admin_allowlist_file":"/missing/allowlist","pepper_keyring_file":"/missing/pepper","native_signing_key_file":"/missing/native-key"}}`
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{name: "valid references do not need available secrets", body: base, ok: true},
		{name: "strict JSON", body: strings.Replace(base, `"version":1`, `"version":1,"surprise":true`, 1)},
		{name: "requires relay listener", body: strings.Replace(base, `,"relay_tcp":{"listen":"/ip4/127.0.0.1/tcp/18082","public_addresses":["/ip4/192.0.2.10/tcp/18082"]}`, "", 1)},
		{name: "requires recovery journal", body: strings.Replace(base, `,"journal":{"region":"us-ashburn-1","namespace":"ns","bucket":"bucket","repository_id":"repo","coverage_floor":0,"coverage_hash":"`+strings.Repeat("0", 64)+`","tenancy_ocid":"ocid1.tenancy.oc1..example","user_ocid":"ocid1.user.oc1..example","fingerprint":"aa:bb","private_key_file":"/missing/journal-key"}`, "", 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := t.TempDir() + "/config.json"
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=TestValidateConfigChecksStaticServingConfigurationWithoutOpeningDependencies")
			cmd.Env = append(os.Environ(), "CLIPP_VALIDATE_HELPER=1", "CLIPP_VALIDATE_CONFIG="+path)
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.ok {
				t.Fatalf("exit err=%v output=%s", err, output)
			}
			if tc.ok && (!strings.Contains(string(output), "configuration accepted") || !strings.Contains(string(output), "operational readiness")) {
				t.Fatalf("validation result did not distinguish acceptance from readiness: %s", output)
			}
		})
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
