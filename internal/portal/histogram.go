package portal

import (
	"fmt"
	"math"
	"math/big"
	"slices"
	"strings"
	"time"
)

// QuotaHistoryEntry is one account-week confirmed recorded usage total.
type QuotaHistoryEntry struct {
	Week     string `json:"week"`
	Recorded int64  `json:"recorded"`
	Observed bool   `json:"observed"`
	Current  bool   `json:"-"`
}

type historyBar struct {
	Week, Label, Value string
	ExactBytes         string
	Recorded           int64
	X, CenterX, Y      float64
	Height, ValueY     float64
	Current, Observed  bool
	Zero, Partial      bool
}

type historyTick struct {
	Label string
	Y     float64
}

type historyHistogram struct {
	Width, Right int
	Bars         []historyBar
	Ticks        []historyTick
	AllZero      bool
	HasCurrent   bool
}

func QuotaHistoryRange(history []QuotaHistoryEntry, all bool) []QuotaHistoryEntry {
	weeks := slices.Clone(history)
	slices.SortFunc(weeks, func(a, b QuotaHistoryEntry) int { return strings.Compare(a.Week, b.Week) })
	if !all && len(weeks) > 4 {
		weeks = weeks[len(weeks)-4:]
	}
	return weeks
}

func quotaHistogram(history []QuotaHistoryEntry) historyHistogram {
	// The account query returns newest first. Copy before sorting so drawing the
	// chronological chart never changes the original profile observation.
	weeks := QuotaHistoryRange(history, true)
	chart := historyHistogram{Width: max(640, 80+64*len(weeks)), AllZero: len(weeks) > 0}
	chart.Right = chart.Width - 16
	var peak int64
	for _, week := range weeks {
		if week.Observed {
			peak = max(peak, week.Recorded)
		} else {
			chart.AllZero = false
		}
	}
	// Binary tick spacing keeps labels in the same units as quota displays.
	// Saturate before doubling to preserve very large integer-byte allowances.
	scale := int64(1024)
	for scale < peak && scale <= math.MaxInt64/2 {
		scale *= 2
	}
	scale = max(scale, peak)
	chart.AllZero = chart.AllZero && peak == 0
	for _, week := range weeks {
		chart.HasCurrent = chart.HasCurrent || week.Current
	}
	for step := int64(0); step <= 4; step++ {
		value := (scale/4)*step + (scale%4)*step/4
		chart.Ticks = append(chart.Ticks, historyTick{FormatBytes(value), 218 - float64(step)*47.5})
	}
	slot := float64(chart.Width-80) / float64(max(1, len(weeks)))
	for index, week := range weeks {
		label := week.Week
		if date, err := time.Parse("2006-01-02", week.Week); err == nil {
			label = date.Format("Jan 2")
		}
		height := float64(week.Recorded) / float64(scale) * 190
		if !week.Observed {
			height = 0
		}
		center := 64 + slot*(float64(index)+0.5)
		chart.Bars = append(chart.Bars, historyBar{
			Week: week.Week, Label: label, Value: FormatBytes(week.Recorded), Recorded: week.Recorded,
			ExactBytes: fmt.Sprint(week.Recorded), Zero: week.Recorded == 0,
			X: center - 18, CenterX: center, Y: 218 - height, Height: height, ValueY: 210 - height,
			Current: week.Current, Observed: week.Observed,
		})
	}
	return chart
}

// quotaMonthlyHistogram groups whole weekly observations by the month in which
// each week begins. Big integers keep aggregate byte totals exact even when
// several individually valid weekly totals exceed int64 when added together.
func quotaMonthlyHistogram(history []QuotaHistoryEntry) historyHistogram {
	type bucket struct {
		month                      string
		total                      *big.Int
		observed, missing, current bool
	}
	var buckets []bucket
	for _, week := range QuotaHistoryRange(history, true) {
		date, err := time.Parse("2006-01-02", week.Week)
		if err != nil {
			continue
		}
		month := date.Format("2006-01")
		if len(buckets) == 0 || buckets[len(buckets)-1].month != month {
			buckets = append(buckets, bucket{month: month, total: new(big.Int)})
		}
		b := &buckets[len(buckets)-1]
		b.current = b.current || week.Current
		if week.Observed {
			b.total.Add(b.total, big.NewInt(week.Recorded))
			b.observed = true
		} else {
			b.missing = true
		}
	}
	chart := historyHistogram{Width: max(640, 80+64*len(buckets)), AllZero: len(buckets) > 0}
	chart.Right = chart.Width - 16
	peak := big.NewInt(0)
	for _, b := range buckets {
		if b.total.Cmp(peak) > 0 {
			peak.Set(b.total)
		}
		chart.HasCurrent = chart.HasCurrent || b.current
		if b.missing {
			chart.AllZero = false
		}
	}
	chart.AllZero = chart.AllZero && peak.Sign() == 0
	scale := big.NewInt(1024)
	for scale.Cmp(peak) < 0 {
		scale.Lsh(scale, 1)
	}
	format := func(n *big.Int) string {
		if n.IsInt64() {
			return FormatBytes(n.Int64())
		}
		f, _ := new(big.Float).SetInt(n).Float64()
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", f/math.Exp2(60)), "0"), ".") + " EiB"
	}
	for step := int64(0); step <= 4; step++ {
		value := new(big.Int).Mul(scale, big.NewInt(step))
		value.Div(value, big.NewInt(4))
		chart.Ticks = append(chart.Ticks, historyTick{format(value), 218 - float64(step)*47.5})
	}
	scaleFloat, _ := new(big.Float).SetInt(scale).Float64()
	slot := float64(chart.Width-80) / float64(max(1, len(buckets)))
	for i, b := range buckets {
		v, _ := new(big.Float).SetInt(b.total).Float64()
		height := v / scaleFloat * 190
		center := 64 + slot*(float64(i)+.5)
		date, _ := time.Parse("2006-01", b.month)
		chart.Bars = append(chart.Bars, historyBar{Week: b.month, Label: date.Format("Jan 2006"), Value: format(b.total), ExactBytes: b.total.String(), Zero: b.total.Sign() == 0, Observed: b.observed, Partial: b.missing && b.observed, Current: b.current, X: center - 18, CenterX: center, Y: 218 - height, Height: height, ValueY: 210 - height})
	}
	return chart
}
