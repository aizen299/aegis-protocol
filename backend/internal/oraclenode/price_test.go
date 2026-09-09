package oraclenode

import (
	"math/big"
	"testing"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

func TestParseScaledExactly(t *testing.T) {
	cases := []struct {
		value    string
		decimals uint8
		want     string
	}{
		{"3000", 18, "3000000000000000000000"},
		{"3000.42", 18, "3000420000000000000000"},
		{"0.000001", 6, "1"},
		{"1", 6, "1000000"},
		{"0", 18, "0"},
		{"0.5", 2, "50"},
		{".5", 2, "50"},
		{"123456789", 0, "123456789"},
	}

	for _, c := range cases {
		got, err := ParseScaled(c.value, c.decimals)
		if err != nil {
			t.Errorf("ParseScaled(%q, %d): %v", c.value, c.decimals, err)
			continue
		}
		if got.String() != c.want {
			t.Errorf("ParseScaled(%q, %d) = %s, want %s", c.value, c.decimals, got, c.want)
		}
	}
}

// A float64 cannot hold 3000.42 exactly. Two nodes parsing the same string through a float would
// disagree in the low digits, and disagreement is what gets a node slashed.
func TestParseScaledDoesNotRouteThroughFloat(t *testing.T) {
	got, err := ParseScaled("3000.42", 18)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// What a float64 round trip produces, for contrast.
	viaFloat, _ := new(big.Float).SetFloat64(3000.42).Mul(
		new(big.Float).SetFloat64(3000.42), new(big.Float).SetInt(pow10(18)),
	).Int(nil)

	if got.String() == viaFloat.String() {
		t.Fatal("the exact parse matched a float round trip; one of them is wrong")
	}
	if got.String() != "3000420000000000000000" {
		t.Fatalf("got %s, want the exact value", got)
	}
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// A source reporting more digits than the feed asks for is normal. Truncating keeps the node
// submitting; rejecting would make it skip rounds for a non-problem.
func TestParseScaledTruncatesExcessPrecision(t *testing.T) {
	got, err := ParseScaled("3000.123456789", 6)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.String() != "3000123456" {
		t.Fatalf("got %s, want 3000.123456 truncated toward zero", got)
	}
}

func TestParseScaledRejectsMalformedInput(t *testing.T) {
	for _, value := range []string{"", "  ", "abc", "-1", "-0.5", "3e18", "3E18", "1.", "1.2.3"} {
		if _, err := ParseScaled(value, 18); err == nil {
			t.Errorf("ParseScaled(%q) should fail", value)
		}
	}
}

// Exponent form is rejected rather than parsed: a source switching to 3e3 mid-flight would
// otherwise change a node's reported price by orders of magnitude without any error.
func TestParseScaledRejectsExponentForm(t *testing.T) {
	if _, err := ParseScaled("3e3", 18); err == nil {
		t.Fatal("exponent form was accepted")
	}
}

func TestFormatScaledRoundTrips(t *testing.T) {
	for _, c := range []struct {
		value    string
		decimals uint8
	}{
		{"3000", 18}, {"3000.42", 18}, {"0.000001", 6}, {"1", 6}, {"0", 18},
	} {
		parsed, err := ParseScaled(c.value, c.decimals)
		if err != nil {
			t.Fatalf("parse %q: %v", c.value, err)
		}

		formatted := FormatScaled(parsed, c.decimals)
		reparsed, err := ParseScaled(formatted, c.decimals)
		if err != nil {
			t.Fatalf("reparse %q: %v", formatted, err)
		}
		if reparsed.String() != parsed.String() {
			t.Errorf("%q -> %s -> %q -> %s", c.value, parsed, formatted, reparsed)
		}
	}
}

func TestFormatScaledBelowOne(t *testing.T) {
	value := types.NewRaw(big.NewInt(1))
	if got := FormatScaled(value, 6); got != "0.000001" {
		t.Fatalf("got %q, want 0.000001", got)
	}
}
