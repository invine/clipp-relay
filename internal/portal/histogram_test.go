package portal

import (
	"math"
	"slices"
	"testing"
	"time"
)

func TestQuotaHistogramPreservesWeeksAndScalesRecordedAmounts(t *testing.T) {
	history := []QuotaHistoryEntry{{Week: "2026-10-05", Recorded: 65536, Observed: true}, {Week: "2026-09-28", Recorded: 12345, Observed: true}, {Week: "2026-09-21", Recorded: 0, Observed: true}}
	original := slices.Clone(history)
	chart := quotaHistogram(history)
	if !slices.Equal(history, original) || len(chart.Bars) != len(history) {
		t.Fatal("histogram changed or dropped a history observation")
	}
	if chart.Bars[0].Week != "2026-09-21" || chart.Bars[2].Week != "2026-10-05" || !chart.Bars[2].Current || chart.Bars[0].Current {
		t.Fatal("weeks are not chronological or current week is incorrect")
	}
	if chart.Bars[0].Height != 0 || chart.Bars[1].Value != "12.06 KiB" || chart.Bars[2].Height != 190 || chart.Ticks[4].Label != "64 KiB" {
		t.Fatalf("incorrect histogram amounts or scale: %+v", chart)
	}
	if math.Abs(chart.Bars[1].Height/chart.Bars[2].Height-float64(12345)/65536) > 1e-12 {
		t.Fatal("bar height does not represent recorded usage proportionally")
	}
	for _, history := range [][]QuotaHistoryEntry{nil, {{Week: "2026-10-05", Observed: true}}, {{Week: "2026-10-05", Recorded: math.MaxInt64, Observed: true}}} {
		chart := quotaHistogram(history)
		for _, bar := range chart.Bars {
			if math.IsNaN(bar.Height) || math.IsInf(bar.Height, 0) || bar.Height < 0 || bar.Height > 190 || bar.Y < 28 {
				t.Fatalf("invalid geometry for zero or maximum quota: %+v", bar)
			}
		}
		if len(history) > 0 && history[0].Recorded == 0 && !chart.AllZero {
			t.Fatal("all-zero history missing its zero-usage state")
		}
	}
}

func TestQuotaHistoryRangeDefaultsToLatestFourAndRetainsAllThirteen(t *testing.T) {
	history := make([]QuotaHistoryEntry, 13)
	for i := range history {
		history[i] = QuotaHistoryEntry{Week: time.Date(2026, 8, 3+i*7, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), Recorded: int64(i), Observed: true}
	}
	latest := QuotaHistoryRange(history, false)
	all := QuotaHistoryRange(history, true)
	if len(latest) != 4 || latest[0].Week != history[9].Week || latest[3].Week != history[12].Week {
		t.Fatalf("latest range = %+v", latest)
	}
	if len(all) != 13 || all[0].Week != history[0].Week || all[12].Week != history[12].Week {
		t.Fatalf("full range = %+v", all)
	}
	if history[0].Recorded != 0 || !history[0].Observed {
		t.Fatal("range selection changed a recorded zero")
	}
}

func TestQuotaHistoryRangeOrdersYearRolloverAndKeepsMissingDistinct(t *testing.T) {
	history := []QuotaHistoryEntry{
		{Week: "2026-01-05", Recorded: math.MaxInt64, Observed: true},
		{Week: "2025-12-29", Recorded: 0, Observed: true},
		{Week: "2025-12-22", Observed: false},
	}
	ranged := QuotaHistoryRange(history, true)
	if len(ranged) != 3 || ranged[0].Week != "2025-12-22" || ranged[1].Week != "2025-12-29" || ranged[2].Week != "2026-01-05" {
		t.Fatalf("year rollover order = %+v", ranged)
	}
	chart := quotaHistogram(ranged)
	if chart.AllZero || chart.Bars[0].Current || chart.Bars[0].Observed || !chart.Bars[1].Observed || !chart.Bars[2].Current || chart.Bars[2].Height != 190 || chart.Ticks[4].Label != FormatBytes(math.MaxInt64) {
		t.Fatalf("missing/zero/large history collapsed: %+v", chart)
	}
}
