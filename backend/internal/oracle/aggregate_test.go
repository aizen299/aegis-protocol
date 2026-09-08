package oracle

import (
	"math/big"
	"testing"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

func raws(t *testing.T, values ...string) []types.Raw {
	t.Helper()
	out := make([]types.Raw, len(values))
	for i, v := range values {
		parsed, err := types.ParseRaw(v)
		if err != nil {
			t.Fatalf("parse %q: %v", v, err)
		}
		out[i] = parsed
	}
	return out
}

func raw(t *testing.T, value string) types.Raw {
	t.Helper()
	return raws(t, value)[0]
}

func TestMedianOfOddCount(t *testing.T) {
	got := Median(raws(t, "3100", "2900", "3000"))
	if got.String() != "3000" {
		t.Fatalf("median = %s, want 3000", got)
	}
}

func TestMedianOfEvenCountFloorsTheMean(t *testing.T) {
	// (2 + 3) / 2 floors to 2, matching Solidity's Math.average.
	if got := Median(raws(t, "1", "2", "3", "4")); got.String() != "2" {
		t.Fatalf("median = %s, want 2 — the mean of the middle pair, floored", got)
	}
}

func TestMedianIsOrderIndependent(t *testing.T) {
	forwards := Median(raws(t, "10", "50", "20", "40", "30"))
	backwards := Median(raws(t, "30", "40", "20", "50", "10"))

	if forwards.String() != backwards.String() {
		t.Fatalf("%s != %s", forwards, backwards)
	}
}

// A settled value near the top of the uint256 range is legitimate. If the Go average overflowed
// where Solidity's does not, the two would disagree and every such round would raise a false alert.
func TestMedianDoesNotOverflowNearMaxUint256(t *testing.T) {
	max := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	nearMax := new(big.Int).Sub(max, big.NewInt(1))

	got := Median([]types.Raw{types.NewRaw(nearMax), types.NewRaw(max)})

	// average(max-1, max) = max-1 + 1/2 = max-1 after flooring.
	if got.String() != nearMax.String() {
		t.Fatalf("median = %s, want %s", got, nearMax)
	}
}

func TestMedianOfEmptyIsZero(t *testing.T) {
	if got := Median(nil); !got.IsZero() {
		t.Fatalf("median = %s, want zero", got)
	}
}

func TestMedianOfSingleValue(t *testing.T) {
	if got := Median(raws(t, "42")); got.String() != "42" {
		t.Fatalf("median = %s", got)
	}
}

// --- deviation ---

func TestDeviationInBasisPoints(t *testing.T) {
	cases := []struct {
		value, median string
		wantBps       int64
	}{
		{"3000", "3000", 0},
		{"3150", "3000", 500},  // +5%
		{"2850", "3000", 500},  // -5%, absolute
		{"3300", "3000", 1000}, // +10%
		{"1500", "3000", 5000}, // -50%
		{"6000", "3000", 10000},
	}

	for _, c := range cases {
		got := Deviation(raw(t, c.value), raw(t, c.median))
		if got.Int64() != c.wantBps {
			t.Errorf("Deviation(%s, %s) = %s bps, want %d", c.value, c.median, got, c.wantBps)
		}
	}
}

// A zero median means the round should never have settled. Treating the deviation as infinite would
// slash every node in it for the contract's mistake.
func TestDeviationFromZeroMedianIsZero(t *testing.T) {
	if got := Deviation(raw(t, "5000"), raw(t, "0")); got.Sign() != 0 {
		t.Fatalf("deviation = %s, want 0", got)
	}
}

// --- outlier detection ---

func TestOutlierDetectionFlagsOnlyBeyondThreshold(t *testing.T) {
	submissions := []types.OracleSubmission{
		{Node: "0xa", Value: raw(t, "3000")},
		{Node: "0xb", Value: raw(t, "3050")}, // +1.67%
		{Node: "0xc", Value: raw(t, "3600")}, // +20%
		{Node: "0xd", Value: raw(t, "1")},    // collapse
	}

	outliers := DetectOutliers(submissions, raw(t, "3000"), 500)
	if len(outliers) != 2 {
		t.Fatalf("flagged %d outliers, want 2: %+v", len(outliers), outliers)
	}
	if outliers[0].Node != "0xc" || outliers[1].Node != "0xd" {
		t.Fatalf("flagged %s and %s", outliers[0].Node, outliers[1].Node)
	}
}

// A submission exactly at the tolerance is inside it. Slashing at the boundary is slashing for
// rounding.
func TestOutlierAtExactThresholdIsNotFlagged(t *testing.T) {
	submissions := []types.OracleSubmission{
		{Node: "0xa", Value: raw(t, "3150")}, // exactly +5%
		{Node: "0xb", Value: raw(t, "2850")}, // exactly -5%
	}

	if outliers := DetectOutliers(submissions, raw(t, "3000"), 500); len(outliers) != 0 {
		t.Fatalf("flagged %d at the exact threshold: %+v", len(outliers), outliers)
	}
}

func TestOutlierDetectionRecordsDeviation(t *testing.T) {
	submissions := []types.OracleSubmission{{Node: "0xa", Value: raw(t, "3600")}}

	outliers := DetectOutliers(submissions, raw(t, "3000"), 500)
	if len(outliers) != 1 {
		t.Fatalf("want one outlier")
	}
	if outliers[0].DeviationBps.Int64() != 2000 {
		t.Fatalf("deviation = %s bps, want 2000 — the audit trail needs the magnitude", outliers[0].DeviationBps)
	}
}

func TestNoOutliersAgainstZeroMedian(t *testing.T) {
	submissions := []types.OracleSubmission{{Node: "0xa", Value: raw(t, "5000")}}

	if outliers := DetectOutliers(submissions, raw(t, "0"), 500); outliers != nil {
		t.Fatalf("flagged %+v against a zero median", outliers)
	}
}

// An honest majority clustered tightly must never be flagged, whatever a single node reports.
func TestHonestClusterIsNeverFlagged(t *testing.T) {
	submissions := []types.OracleSubmission{
		{Node: "0xa", Value: raw(t, "3000")},
		{Node: "0xb", Value: raw(t, "3001")},
		{Node: "0xc", Value: raw(t, "2999")},
		{Node: "0xd", Value: raw(t, "3002")},
		{Node: "0xe", Value: raw(t, "1")},
	}

	median := Median([]types.Raw{
		raw(t, "3000"), raw(t, "3001"), raw(t, "2999"), raw(t, "3002"), raw(t, "1"),
	})
	outliers := DetectOutliers(submissions, median, 500)

	if len(outliers) != 1 || outliers[0].Node != "0xe" {
		t.Fatalf("flagged %+v, want only the manipulated node", outliers)
	}
}
