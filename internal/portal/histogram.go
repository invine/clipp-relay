package portal

import (
	"math"
	"slices"
	"strings"
	"time"
)

// QuotaHistoryEntry is one account-week confirmed recorded usage total.
type QuotaHistoryEntry struct {
	Week     string `json:"week"`
	Recorded int64  `json:"recorded"`
}

type historyBar struct {
	Week, Label, Value string
	Recorded           int64
	X, CenterX, Y      float64
	Height, ValueY     float64
	Current            bool
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
}

func quotaHistogram(history []QuotaHistoryEntry) historyHistogram {
	// The account query returns newest first. Copy before sorting so drawing the
	// chronological chart never changes the original profile observation.
	weeks := slices.Clone(history)
	slices.SortFunc(weeks, func(a, b QuotaHistoryEntry) int { return strings.Compare(a.Week, b.Week) })
	chart := historyHistogram{Width: max(640, 80+64*len(weeks)), AllZero: true}
	chart.Right = chart.Width - 16
	var peak int64
	for _, week := range weeks {
		peak = max(peak, week.Recorded)
	}
	// Binary tick spacing keeps labels in the same units as quota displays.
	// Saturate before doubling to preserve very large integer-byte allowances.
	scale := int64(1024)
	for scale < peak && scale <= math.MaxInt64/2 {
		scale *= 2
	}
	scale = max(scale, peak)
	chart.AllZero = peak == 0
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
		center := 64 + slot*(float64(index)+0.5)
		chart.Bars = append(chart.Bars, historyBar{
			Week: week.Week, Label: label, Value: FormatBytes(week.Recorded), Recorded: week.Recorded,
			X: center - 18, CenterX: center, Y: 218 - height, Height: height, ValueY: 210 - height,
			Current: index == len(weeks)-1,
		})
	}
	return chart
}
