package utils

import "math"

// ApplyMultiplier applies the model billing multiplier to a base amount.
// Estimated charges should round up to avoid under-pre-deduction, while
// final settlement rounds to the nearest integer for a stable账单结果.
func ApplyMultiplier(baseAmount int64, multiplier float32, roundUp bool) int64 {
	if baseAmount <= 0 {
		return 0
	}

	scaled := float64(baseAmount) * float64(multiplier)
	if roundUp {
		return int64(math.Ceil(scaled))
	}

	return int64(math.Round(scaled))
}
