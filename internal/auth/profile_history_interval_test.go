package auth_test

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOwnerHistoryIntervalAndMonthlyGrouping(t *testing.T) {
	p := protectedPortalFixture(t)
	now := time.Now().UTC()
	week := now.AddDate(0, 0, -(int(now.Weekday())+6)%7)
	week = time.Date(week.Year(), week.Month(), week.Day(), 0, 0, 0, 0, time.UTC)
	old := week.AddDate(0, 0, -7)
	seedRecordedWeek(t, p, old, 12345)
	seedRecordedWeek(t, p, week, 1024)
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
	expanded := portalRequest(p.auth, "GET", "/?history_rows=14", p.owner, nil, "")
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
