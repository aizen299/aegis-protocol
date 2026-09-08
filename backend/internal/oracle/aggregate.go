package oracle

import (
	"math/big"
	"sort"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// The service medianizes independently of the contract rather than trusting the settled value.
// A disagreement between the two is an alert, never a correction: the chain is authoritative, and
// a backend that "fixed" a settled price would be asserting authority the staking and slashing
// design deliberately denies it.

// Median returns the middle value, or the mean of the two middle values for an even count.
//
// Mirrors OracleRounds._median exactly, including the rounding. Math.average in Solidity floors,
// and computes a + (b-a)/2 to avoid overflowing near 2^256 — reproduced here so a legitimate value
// near the top of the range cannot make the two disagree.
func Median(values []types.Raw) types.Raw {
	if len(values) == 0 {
		return types.Raw{}
	}

	sorted := make([]*big.Int, len(values))
	for i, v := range values {
		sorted[i] = v.Big()
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Cmp(sorted[j]) < 0 })

	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return types.NewRaw(sorted[mid])
	}
	return types.NewRaw(average(sorted[mid-1], sorted[mid]))
}

// average computes floor((a+b)/2) without an intermediate that can overflow a uint256, matching
// OpenZeppelin's Math.average.
func average(a, b *big.Int) *big.Int {
	low, high := a, b
	if low.Cmp(high) > 0 {
		low, high = high, low
	}

	diff := new(big.Int).Sub(high, low)
	return new(big.Int).Add(low, diff.Rsh(diff, 1))
}

// Deviation reports how far value sits from median, in basis points.
//
// Returns 0 when the median is zero: a relative deviation from nothing is undefined, and treating
// it as infinite would slash every node in a round the contract should never have settled.
func Deviation(value, median types.Raw) *big.Int {
	m := median.Big()
	if m.Sign() == 0 {
		return new(big.Int)
	}

	diff := new(big.Int).Sub(value.Big(), m)
	diff.Abs(diff)

	return diff.Mul(diff, big.NewInt(10_000)).Div(diff, m)
}

// Outlier is a submission far enough from the median to be slashable.
type Outlier struct {
	Node         string
	Value        types.Raw
	DeviationBps *big.Int
}

// DetectOutliers flags submissions deviating beyond thresholdBps from the median.
//
// Comparison is strictly greater than the threshold, so a submission exactly at the boundary is
// not slashed. A penalty applied at the edge of a tolerance is a penalty applied to rounding.
func DetectOutliers(submissions []types.OracleSubmission, median types.Raw, thresholdBps int64) []Outlier {
	if median.IsZero() {
		return nil
	}

	threshold := big.NewInt(thresholdBps)
	var outliers []Outlier

	for _, sub := range submissions {
		deviation := Deviation(sub.Value, median)
		if deviation.Cmp(threshold) > 0 {
			outliers = append(outliers, Outlier{
				Node:         sub.Node,
				Value:        sub.Value,
				DeviationBps: deviation,
			})
		}
	}
	return outliers
}
