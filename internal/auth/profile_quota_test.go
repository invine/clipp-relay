package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"clipp-relay/internal/auth"
)

func TestOwnerProfileShowsRecordedUsageAndAbsoluteHistory(t *testing.T) {
	c, m, db := fixture(t)
	d := newProvider(t)
	s := newTestServer(t, db.Pool, c, m, d.endpoints())
	subject := "quota-profile-" + time.Now().UTC().Format("20060102150405.000000000")
	cookie := loginAs(t, s, d, subject, subject+"@example.test", "")
	var id string
	if err := db.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, subject).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET status='Active',recovery_review_hold=false,plan_id='6dd09395-51a0-451c-96b3-716e6038e870' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	q := newQuota(db.Pool)
	t.Cleanup(q.Close)
	if _, err := q.Eligibility(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if err := q.Observe(id, 10*1024); err != nil {
		t.Fatal(err)
	}
	if err := q.FlushAccounting(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO public.weekly_relay_usage(account_id,week_start,recorded_bytes,sequence,latest_operation_id,latest_delta_bytes) VALUES($1,(date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-7),12345,1,$2,12345) ON CONFLICT(account_id,week_start) DO UPDATE SET recorded_bytes=EXCLUDED.recorded_bytes,sequence=EXCLUDED.sequence,latest_operation_id=EXCLUDED.latest_operation_id,latest_delta_bytes=EXCLUDED.latest_delta_bytes`, id, "386dfe14-2f37-45e7-ace8-0cbcc3851469"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO public.weekly_relay_usage(account_id,week_start,recorded_bytes,sequence,latest_operation_id,latest_delta_bytes) VALUES($1,(date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC')::date-14),0,0,NULL,0) ON CONFLICT(account_id,week_start) DO UPDATE SET recorded_bytes=0,sequence=0,latest_operation_id=NULL,latest_delta_bytes=0`, id); err != nil {
		t.Fatal(err)
	}
	out := portalRequest(s, "GET", "/", cookie, nil, "")
	body := out.Body.String()
	if out.Code != 200 || !strings.Contains(body, "Recorded Relay Usage: 10 KiB") || !strings.Contains(body, "12.06 KiB") || !strings.Contains(body, "periodic confirmation lag") || !strings.Contains(body, "Login Grants") || !strings.Contains(body, "Weekly Quota Window") || !strings.Contains(body, "Refresh account state") || !strings.Contains(body, "Unavailable") {
		t.Fatalf("profile: %d %s", out.Code, body)
	}
	if !strings.Contains(body, "Current week is incomplete") || !strings.Contains(body, "10 KiB <span class=\"exact-bytes\">(10240 bytes)</span>") || !strings.Contains(body, "No observation") || !strings.Contains(body, "Recorded zero") || !strings.Contains(body, `class="history-column missing"`) || !strings.Contains(body, "no recorded observation") {
		t.Fatalf("history states or exact values missing: %s", body)
	}
	historyTable := func(html string) string {
		start := strings.Index(html, `class="history-table"`)
		if start < 0 {
			return ""
		}
		end := strings.Index(html[start:], "</table>")
		if end < 0 {
			return ""
		}
		return html[start : start+end]
	}
	if got := strings.Count(historyTable(body), "<tr>"); got != 5 {
		t.Fatalf("latest range should expose four weekly rows plus its header, got %d", got)
	}
	all := portalRequest(s, "GET", "/?history=all", cookie, nil, "")
	if all.Code != 200 || strings.Count(historyTable(all.Body.String()), "<tr>") != 14 || !strings.Contains(all.Body.String(), `href="/">Latest four weeks</a>`) {
		t.Fatalf("all retained history selection: %d %s", all.Code, all.Body.String())
	}
	if strings.Index(body, "Your allowance") > strings.Index(body, "Add this relay to Clipp") || strings.Contains(body, "Device Network") || strings.Contains(body, "connected devices") || !strings.Contains(body, `href="/account/security"`) || !strings.Contains(body, "<details class=\"workspace-menu\">") || !strings.Contains(body, "portal.example.test") {
		t.Fatalf("account facts are not leading or imply device connectivity: %s", body)
	}
	var grantID string
	if err := db.Pool.QueryRow(context.Background(), `SELECT md5($1)::uuid::text`, subject).Scan(&grantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `INSERT INTO public.native_login_grants(id,account_id,credential_generation,client_type,created_at,last_used_at,idle_expires_at,absolute_expires_at) SELECT $1,id,credential_generation,'android',clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 days',clock_timestamp()+interval '180 days' FROM public.accounts WHERE id=$2`, grantID, id); err != nil {
		t.Fatal(err)
	}
	s.SetCapacitySampler(s.SampleActiveGrants)
	withGrant := portalRequest(s, "GET", "/", cookie, nil, "")
	if withGrant.Code != 200 || !strings.Contains(withGrant.Body.String(), "1 of 20") || !strings.Contains(withGrant.Body.String(), "Unavailable") {
		t.Fatalf("live grant count: %d %s", withGrant.Code, withGrant.Body.String())
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.native_login_grants SET terminated_at=clock_timestamp() WHERE id=$1`, grantID); err != nil {
		t.Fatal(err)
	}
	withoutGrant := portalRequest(s, "GET", "/", cookie, nil, "")
	if withoutGrant.Code != 200 || !strings.Contains(withoutGrant.Body.String(), "0 of 20") {
		t.Fatalf("terminated grant count: %d %s", withoutGrant.Code, withoutGrant.Body.String())
	}
	live, grants := 2, 3
	s.SetCapacitySampler(func(context.Context, string) (auth.CapacitySample, error) {
		return auth.CapacitySample{LiveSessions: &live, ActiveLoginGrants: &grants}, nil
	})
	withCounts := portalRequest(s, "GET", "/", cookie, nil, "")
	if withCounts.Code != 200 || !strings.Contains(withCounts.Body.String(), "2 of 5") || !strings.Contains(withCounts.Body.String(), "3 of 20") {
		t.Fatalf("aggregate counts: %d %s", withCounts.Code, withCounts.Body.String())
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
	if reduced := portalRequest(s, "GET", "/", cookie, nil, ""); reduced.Code != 200 || !strings.Contains(reduced.Body.String(), "exceeds the current allowance") || !strings.Contains(reduced.Body.String(), "10240 bytes") || !strings.Contains(reduced.Body.String(), "12345 bytes") {
		t.Fatalf("reduced allowance: %d %s", reduced.Code, reduced.Body.String())
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE public.accounts SET plan_id='0d083fee-7828-4ebf-8eef-70a18e8cfcdb' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if zero := portalRequest(s, "GET", "/", cookie, nil, ""); zero.Code != 200 || !strings.Contains(zero.Body.String(), "Zero allowance") || !strings.Contains(zero.Body.String(), "exceeds the current allowance") {
		t.Fatalf("zero allowance: %d %s", zero.Code, zero.Body.String())
	}
}
