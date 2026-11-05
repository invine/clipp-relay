package auth_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOwnerMonthlyHistoryTotalsAndAccountIsolation(t *testing.T) {
	p := protectedPortalFixture(t)
	now := time.Now().UTC()
	week := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(int(now.Weekday())+6)%7)
	// Pick two adjacent retained weeks with the same UTC start month.
	for week.Month() != week.AddDate(0, 0, -7).Month() {
		week = week.AddDate(0, 0, -7)
	}
	earlier := week.AddDate(0, 0, -7)
	seedRecordedWeek(t, p, earlier, 1024)
	seedRecordedWeek(t, p, week, 2048)
	seedRecordedWeek(t, p, earlier.AddDate(0, 0, -7), 16384)
	subject := fmt.Sprintf("history-other-%d", time.Now().UnixNano())
	loginAs(t, p.auth, p.provider, subject, "history-other@example.test", "")
	other := p
	if err := p.runtime.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, subject).Scan(&other.account); err != nil {
		t.Fatal(err)
	}
	seedRecordedWeek(t, other, earlier, 65536)
	path := fmt.Sprintf("/?start=%s&end=%s&group=monthly", earlier.Format("2006-01-02"), week.Format("2006-01-02"))
	response := portalRequest(p.auth, "GET", path, p.owner, nil, "")
	body := response.Body.String()
	for _, want := range []string{fmt.Sprintf("Month %s UTC: 3 KiB (3072 bytes)", week.Format("2006-01")), "(1024 bytes)", "(2048 bytes)"} {
		if response.Code != 200 || !strings.Contains(body, want) {
			t.Fatalf("monthly owner history missing %q (status %d)", want, response.Code)
		}
	}
	for _, unwanted := range []string{"(16384 bytes)", "(65536 bytes)", "(68608 bytes)"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("monthly owner history exposed out-of-interval or another account's usage: %s", unwanted)
		}
	}
}

func TestOwnerHistoryIntervalAndMonthlyGrouping(t *testing.T) {
	p := protectedPortalFixture(t)
	now := time.Now().UTC()
	week := now.AddDate(0, 0, -(int(now.Weekday())+6)%7)
	week = time.Date(week.Year(), week.Month(), week.Day(), 0, 0, 0, 0, time.UTC)
	old := week.AddDate(0, 0, -7)
	seedRecordedWeek(t, p, old, 12345)
	seedRecordedWeek(t, p, week, 1024)
	defaultView := portalRequest(p.auth, "GET", "/", p.owner, nil, "")
	defaultBody := defaultView.Body.String()
	if defaultView.Code != 200 || !strings.Contains(defaultBody, "Usage History") || !strings.Contains(defaultBody, `value="`+week.AddDate(0, 0, -21).Format("2006-01-02")+`"`) || len(historyDate.FindAllStringSubmatch(defaultBody, -1)) != 4 {
		t.Fatal("default history should include the current week and three preceding weeks")
	}
	if strings.Contains(defaultBody, "Start date (UTC)") || strings.Contains(defaultBody, "End date (UTC)") || strings.Index(defaultBody, `class="history-interval"`) < strings.Index(defaultBody, `class="history-table"`) {
		t.Fatal("interval controls should follow the table and use the requested labels")
	}
	start, end := old.Format("2006-01-02"), week.Format("2006-01-02")
	path := fmt.Sprintf("/?start=%s&end=%s&group=monthly", start, end)
	resp := portalRequest(p.auth, "GET", path, p.owner, nil, "")
	body := resp.Body.String()
	for _, want := range []string{start, end, "Monthly", "month containing each week", "12345 bytes", "1024 bytes"} {
		if resp.Code != 200 || !strings.Contains(body, want) {
			t.Fatalf("monthly history missing %q, status %d", want, resp.Code)
		}
	}
	for _, query := range []string{"start=2026-02-30&end=2026-03-01", "start=2026-03-01&end=2026-02-28", "start=2025-01-01&end=2026-01-02", "start=2026-01-01"} {
		out := portalRequest(p.auth, "GET", "/?"+query, p.owner, nil, "")
		if out.Code != 400 || !strings.Contains(out.Body.String(), "Choose valid") {
			t.Fatalf("invalid interval %q: %d", query, out.Code)
		}
	}
	for _, query := range []string{"start=2025-01-01&end=2026-01-01", "start=2024-02-29&end=2025-02-28"} {
		out := portalRequest(p.auth, "GET", "/?"+query, p.owner, nil, "")
		if out.Code != 200 {
			t.Fatalf("valid boundary %q: %d", query, out.Code)
		}
	}
	empty := portalRequest(p.auth, "GET", "/?start=2000-01-01&end=2000-12-31", p.owner, nil, "")
	if empty.Code != 200 || !strings.Contains(empty.Body.String(), "No quota history available") || strings.Contains(empty.Body.String(), `class="history-histogram"`) {
		t.Fatal("empty interval did not show an honest empty state")
	}
	// A new interval submission has no pagination parameter; it restores the
	// first four rows regardless of how far the preceding interval was opened.
	expanded := portalRequest(p.auth, "GET", "/?start="+week.AddDate(0, 0, -98).Format("2006-01-02")+"&end="+end+"&history_rows=14", p.owner, nil, "")
	if got := len(historyDate.FindAllStringSubmatch(expanded.Body.String(), -1)); got != 14 {
		t.Fatalf("expanded table has %d rows", got)
	}
	intervalStart := week.AddDate(0, 0, -7*8).Format("2006-01-02")
	reset := portalRequest(p.auth, "GET", "/?start="+intervalStart+"&end="+end, p.owner, nil, "")
	if got := len(historyDate.FindAllStringSubmatch(reset.Body.String(), -1)); got != 4 {
		t.Fatalf("new interval should reset table to four rows, got %d", got)
	}
	if !strings.Contains(reset.Body.String(), intervalStart) || strings.Contains(reset.Body.String(), week.AddDate(0, 0, -7*9).Format("2006-01-02")) {
		t.Fatal("graph or table escaped selected week-start interval")
	}
}
