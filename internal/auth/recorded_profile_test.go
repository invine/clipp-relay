package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/quota"
)

func TestObservedOwnerAndAdminShowConfirmedUsageAndHistory(t *testing.T) {
	p := protectedPortalFixture(t)
	ctx := context.Background()
	op, err := quota.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	week := now.AddDate(0, 0, -(int(now.Weekday())+6)%7)
	week = time.Date(week.Year(), week.Month(), week.Day(), 0, 0, 0, 0, time.UTC)
	if _, err = p.quota.Settle(ctx, quota.Settlement{AccountID: p.account, WeekStart: week, OperationID: op, Sequence: 1, DeltaBytes: 12345}); err != nil {
		t.Fatal(err)
	}
	previous, err := quota.NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.quota.Settle(ctx, quota.Settlement{AccountID: p.account, WeekStart: week.AddDate(0, 0, -7), OperationID: previous, Sequence: 1, DeltaBytes: 54321}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.quota.Eligibility(ctx, p.account, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = p.auth.Pool.Exec(ctx, `UPDATE public.accounts SET weekly_bytes_override=1000 WHERE id=$1`, p.account); err != nil {
		t.Fatal(err)
	}
	p.auth.WithAccountGuards(ctx, []string{p.account}, func(context.Context) {
		if err = p.quota.Observe(p.account, 10000); err != nil {
			t.Fatal(err)
		}
		for _, view := range []struct {
			path  string
			admin bool
		}{{"/", false}, {"/admin?selected=" + p.account, true}} {
			cookie := p.owner
			if view.admin {
				cookie = p.admin
			}
			out := portalRequest(p.auth, "GET", view.path, cookie, nil, "")
			body := out.Body.String()
			for _, want := range []string{"Recorded Relay Usage", "12.06 KiB", "confirmation lag", "HOP and STOP", "current week + twelve", "53.05 KiB", "Remaining allowance: 0 bytes"} {
				if out.Code != 200 || !strings.Contains(body, want) {
					t.Fatalf("%s missing %q: %d %s", view.path, want, out.Code, body)
				}
			}
			if strings.Contains(body, "21.82 KiB") {
				t.Fatal("pending observations presented as recorded")
			}
			if strings.Contains(body, "unused funded accounting") {
				t.Fatal("observed view retained prepaid wording")
			}
		}
	})
	client := protectedClient(t, p)
	requireProtectedAuthentication(t, client, p, `"code":"quota_exhausted"`)

}
