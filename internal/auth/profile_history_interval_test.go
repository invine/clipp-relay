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
	subject := fmt.Sprintf("history-other-%d", time.Now().UnixNano())
	loginAs(t, p.auth, p.provider, subject, "history-other@example.test", "")
	other := p
	if err := p.runtime.Pool.QueryRow(context.Background(), `SELECT id FROM public.accounts WHERE subject=$1`, subject).Scan(&other.account); err != nil {
		t.Fatal(err)
	}
	seedRecordedWeek(t, other, earlier, 65536)
	path := "/?group=monthly"
	response := portalRequest(p.auth, "GET", path, p.owner, nil, "")
	body := response.Body.String()
	for _, want := range []string{fmt.Sprintf("Month %s UTC: 3 KiB (3072 bytes)", week.Format("2006-01")), "(3072 bytes)"} {
		if response.Code != 200 || !strings.Contains(body, want) {
			t.Fatalf("monthly owner history missing %q (status %d)", want, response.Code)
		}
	}
	for _, unwanted := range []string{"(65536 bytes)", "(68608 bytes)"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("monthly owner history exposed out-of-interval or another account's usage: %s", unwanted)
		}
	}
}

func TestOwnerHistoryGroupingAndDisclosure(t *testing.T) {
	p := protectedPortalFixture(t)
	now := time.Now().UTC()
	week := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -(int(now.Weekday())+6)%7)
	seedRecordedWeek(t, p, week, 1024)
	seedRecordedWeek(t, p, week.AddDate(0, 0, -7), 12345)
	weekly := portalRequest(p.auth, "GET", "/", p.owner, nil, "")
	body := weekly.Body.String()
	for _, want := range []string{"Usage History", `class="history-axis-fixed"`, `class="history-table-details"`, "Weekly usage table", "12345 bytes", "No observation"} {
		if weekly.Code != 200 || !strings.Contains(body, want) {
			t.Fatalf("weekly history missing %q: status %d", want, weekly.Code)
		}
	}
	for _, unwanted := range []string{`type="date"`, ">Observation</th>", `<details class="history-table-details" open`} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("unexpected history control %q", unwanted)
		}
	}
	if strings.Index(body, "data-history-more") < strings.Index(body, `<details class="history-table-details">`) || strings.Index(body, "data-history-more") > strings.Index(body, "</details>\n<span class=\"sr-only\" data-history-status") {
		t.Fatal("Show more must be inside the folded history table")
	}
	if !strings.Contains(body, "oldest to newest. Scroll left") {
		t.Fatal("graph direction should describe the new order and loading direction")
	}
	monthly := portalRequest(p.auth, "GET", "/?group=monthly", p.owner, nil, "")
	if monthly.Code != 200 || !strings.Contains(monthly.Body.String(), "Monthly usage table") || !strings.Contains(monthly.Body.String(), `Month</th>`) || strings.Contains(monthly.Body.String(), `Week starting UTC</th>`) {
		t.Fatalf("monthly graph and table grouping: %d", monthly.Code)
	}
}
