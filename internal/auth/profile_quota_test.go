package auth_test

import (
	"context"
	"strings"
	"testing"

	"clipp-relay/internal/auth"
	"clipp-relay/internal/quota"
)

func TestOwnerProfileShowsCommittedUsageAndAbsoluteHistory(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := auth.New(db.Pool, c, m, d.endpoints())
	cookie := loginAs(t, s, d, "quota-profile", "quota-profile@example.test", "")
	var id string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject='quota-profile'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	q := quota.New(db.Pool)
	t.Cleanup(q.Close)
	if _, err := q.Take(context.Background(), id, 0, 10*1024); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO public.weekly_quota_usage(account_id,week_start,committed_bytes,sequence,latest_operation_id,latest_granted_bytes) VALUES($1,(date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-7),12345,1,$2,12345)`, id, "386dfe14-2f37-45e7-ace8-0cbcc3851469"); err != nil {
		t.Fatal(err)
	}
	out := portalRequest(s, "GET", "/", cookie, nil, "")
	body := out.Body.String()
	if out.Code != 200 || !strings.Contains(body, "Quota committed: 65536 bytes") || !strings.Contains(body, "12345 bytes") || !strings.Contains(body, "includes unused funded credit") || strings.Contains(body, "Quota committed: 10240 bytes") {
		t.Fatalf("profile: %d %s", out.Code, body)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO public.quota_plans(id,name,weekly_bytes,sessions) VALUES('0d083fee-7828-4ebf-8eef-70a18e8cfcda','Reduced profile',1024,5),('0d083fee-7828-4ebf-8eef-70a18e8cfcdb','Zero profile',0,0)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(context.Background(), `UPDATE public.accounts SET plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE id=$1`, id)
		_, _ = db.Pool.Exec(context.Background(), `DELETE FROM public.quota_plans WHERE id IN ('0d083fee-7828-4ebf-8eef-70a18e8cfcda','0d083fee-7828-4ebf-8eef-70a18e8cfcdb')`)
	})
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET plan_id='0d083fee-7828-4ebf-8eef-70a18e8cfcda' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if reduced := portalRequest(s, "GET", "/", cookie, nil, ""); reduced.Code != 200 || !strings.Contains(reduced.Body.String(), "exceeds the current allowance") {
		t.Fatalf("reduced allowance: %d %s", reduced.Code, reduced.Body.String())
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET plan_id='0d083fee-7828-4ebf-8eef-70a18e8cfcdb' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if zero := portalRequest(s, "GET", "/", cookie, nil, ""); zero.Code != 200 || !strings.Contains(zero.Body.String(), "Zero allowance") || !strings.Contains(zero.Body.String(), "exceeds the current zero allowance") {
		t.Fatalf("zero allowance: %d %s", zero.Code, zero.Body.String())
	}
}
