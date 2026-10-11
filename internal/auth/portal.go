package auth

import (
	"errors"
	"math/big"
	"regexp"
)

var quotaQuantity = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

// Administrator forms accept decimal binary units but store an exact, whole
// number of bytes. An omitted unit retains compatibility with byte-only forms.
func parseQuotaBytes(value, unit string) (int64, error) {
	factors := map[string]int64{"": 1, "B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}
	factor, known := factors[unit]
	if !known || len(value) > 32 || !quotaQuantity.MatchString(value) {
		return 0, errors.New("invalid quota quantity")
	}
	quantity, ok := new(big.Rat).SetString(value)
	if !ok {
		return 0, errors.New("invalid quota quantity")
	}
	quantity.Mul(quantity, new(big.Rat).SetInt64(factor))
	if !quantity.IsInt() || !quantity.Num().IsInt64() {
		return 0, errors.New("quota must be a whole number of bytes within int64")
	}
	return quantity.Num().Int64(), nil
}
