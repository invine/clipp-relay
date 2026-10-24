package portal

import (
	"math"
	"slices"
	"testing"
)

func TestQuotaHistogramPreservesWeeksAndScalesRecordedAmounts(t *testing.T) {
	history := []QuotaHistoryEntry{{"2026-10-05", 65536}, {"2026-09-28", 12345}, {"2026-09-21", 0}}
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
	for _, history := range [][]QuotaHistoryEntry{nil, {{"2026-10-05", 0}}, {{"2026-10-05", math.MaxInt64}}} {
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
