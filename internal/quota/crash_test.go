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

func TestAllocationProcessCrashAtCommitBoundary(t *testing.T) {
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
			q.beforeCommit = func(context.Context, pgx.Tx) error { hold(); return nil }
		case "after":
			q.afterCommit = func() error { hold(); return nil }
		default:
			t.Fatalf("unknown crash mode %q", mode)
		}
		_, _ = q.Take(context.Background(), os.Getenv("CLIPP_CRASH_ACCOUNT"), 0, 1024)
		t.Fatal("allocation returned before process kill")
	}

	for _, tc := range []struct {
		mode      string
		durable   int64
		restarted int64
	}{
		{"before", 0, BlockBytes},
		{"after", BlockBytes, 2 * BlockBytes},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			_, id, pool := fixture(t)
			child := exec.Command(os.Args[0], "-test.run=^TestAllocationProcessCrashAtCommitBoundary$")
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
			if err := pool.QueryRow(ctx, `SELECT COALESCE((SELECT committed_bytes FROM public.weekly_quota_usage WHERE account_id=$1),0)`, id).Scan(&durable); err != nil || durable != tc.durable {
				t.Fatalf("durable debit after %s crash = %d, want %d: %v", tc.mode, durable, tc.durable, err)
			}
			restarted := New(pool)
			defer restarted.Close()
			got, err := restarted.Take(ctx, id, 0, 1024)
			if err != nil || got.Committed != tc.restarted || got.Usable != BlockBytes-1024 {
				t.Fatalf("fresh process credit after %s crash = %+v, want committed %d and usable %d: %v", tc.mode, got, tc.restarted, BlockBytes-1024, err)
			}
		})
	}
}
