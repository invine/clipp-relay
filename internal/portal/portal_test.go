package portal

import (
	"math"
	"strconv"
	"testing"
)

func TestByteDisplaysAndEditValues(t *testing.T) {
	for _, tc := range []struct {
		value int64
		want  string
	}{{0, "0 bytes"}, {512, "512 bytes"}, {1024, "1 KiB"}, {12345, "12.06 KiB"}, {65536, "64 KiB"}, {1 << 20, "1 MiB"}, {1 << 30, "1 GiB"}, {1536 << 20, "1.5 GiB"}, {1 << 40, "1 TiB"}, {math.MaxInt64, "8 EiB"}} {
		if got := FormatBytes(tc.value); got != tc.want {
			t.Fatalf("FormatBytes(%d) = %s, want %s", tc.value, got, tc.want)
		}
	}
	// An untouched administrator override must preserve every byte, including
	// arbitrary limits and values too large to round-trip through float64.
	for _, value := range []int64{0, 12345, 1 << 30, 1536 << 20, math.MaxInt64} {
		input := byteInput(strconv.FormatInt(value, 10))
		quantity, err := strconv.ParseInt(input.Value, 10, 64)
		factor := map[string]int64{"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}[input.Unit]
		if err != nil || factor == 0 || quantity*factor != value {
			t.Fatalf("quota edit changed %d to %+v", value, input)
		}
	}
}
