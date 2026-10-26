package quota

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/config"
	"clipp-relay/internal/database"
	"github.com/jackc/pgx/v5"
)

func TestObservedProcessCrashAtCommitBoundary(t *testing.T) {
	if mode := os.Getenv("CLIPP_CRASH_MODE"); mode != "" {
		path := os.Getenv("CLIPP_TEST_SERVING_CONFIG")
		c, err := config.Load(path)
		if err != nil || c.Database.Host != "localhost" || !strings.HasPrefix(c.Database.Name, "clipp_ticket02_") {
			t.Fatalf("unsafe child fixture: %v", err)
		}
		material, err := c.ReadMaterial()
		if err != nil {
			t.Fatal(err)
		}
		pool, err := database.NewPool(context.Background(), c, material, false)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		q := New(pool)
		defer q.Close()
		hold := func() {
			_, _ = fmt.Fprintln(os.Stdout, "at-commit-boundary")
			select {}
		}
		switch mode {
		case "before":
			q.SetSettlementHooksForTest(func(context.Context, pgx.Tx) error { hold(); return nil }, nil)
		case "after":
			q.SetSettlementHooksForTest(nil, func(context.Context) error { hold(); return nil })
		default:
			t.Fatalf("unknown crash mode %q", mode)
		}
		_, _ = q.Eligibility(context.Background(), os.Getenv("CLIPP_CRASH_ACCOUNT"))
		_ = q.Observe(os.Getenv("CLIPP_CRASH_ACCOUNT"), 12345)
		_ = q.FlushAccounting(context.Background(), os.Getenv("CLIPP_CRASH_ACCOUNT"))
		t.Fatal("settlement returned before process kill")
	}

	for _, tc := range []struct {
		mode      string
		durable   int64
		restarted int64
	}{
		{"before", 0, 0},
		{"after", 12345, 12345},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			_, id, pool := fixture(t)
			child := exec.Command(os.Args[0], "-test.run=^TestObservedProcessCrashAtCommitBoundary$")
			child.Env = append(os.Environ(), "CLIPP_CRASH_MODE="+tc.mode, "CLIPP_CRASH_ACCOUNT="+id)
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			child.Stderr = &stderr
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			line := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(stdout)
				if scanner.Scan() {
					line <- scanner.Text()
				} else {
					line <- ""
				}
			}()
			select {
			case got := <-line:
				if got != "at-commit-boundary" {
					_ = child.Process.Kill()
					_ = child.Wait()
					t.Fatalf("child stopped before boundary: %q stderr=%s", got, stderr.String())
				}
			case <-time.After(5 * time.Second):
				_ = child.Process.Kill()
				_ = child.Wait()
				t.Fatalf("child never reached boundary: %s", stderr.String())
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var durable int64
			if err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT recorded_bytes FROM public.weekly_relay_usage WHERE account_id=$1),0)`, id).Scan(&durable); err != nil || durable != tc.durable {
				t.Fatalf("durable debit after %s crash = %d, want %d: %v", tc.mode, durable, tc.durable, err)
			}
			restarted := New(pool)
			defer restarted.Close()
			got, err := restarted.Eligibility(ctx, id)
			if err != nil || got.RecordedBytes != tc.restarted || got.EffectiveBytes != tc.restarted {
				t.Fatalf("restart after %s = %+v: %v", tc.mode, got, err)
			}
			// A graceful no-observation flush after restart creates no debit.
			if err := restarted.FlushAllAccounting(ctx); err != nil {
				t.Fatal(err)
			}
			usage, err := restarted.RecordedUsage(ctx, id, weekStart(time.Now()))
			if err != nil || usage.RecordedBytes != tc.restarted {
				t.Fatalf("restart flush=%+v %v", usage, err)
			}
		})
	}
}
