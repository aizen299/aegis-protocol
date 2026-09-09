// Package oraclenode is the off-chain half of the oracle: it fetches prices from independent
// sources, reduces them to one value, signs it, and submits it to the round that is open.
package oraclenode

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// ParseScaled converts a decimal price string into raw base units at the feed's scale.
//
// Deliberately not via float64. A price like 3000.42 has no exact binary representation, and the
// error compounds through medianization into a value that disagrees with what every other node
// computed from the same input — which reads as an outlier and gets the node slashed.
//
// Extra precision beyond the feed's scale is truncated toward zero rather than rejected. A source
// reporting more digits than the feed asks for is normal, and refusing it would make the node skip
// rounds for a non-problem.
func ParseScaled(value string, decimals uint8) (types.Raw, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return types.Raw{}, fmt.Errorf("price is empty")
	}
	if strings.ContainsAny(trimmed, "eE") {
		return types.Raw{}, fmt.Errorf("price %q is in exponent form; a source must report plain decimal", value)
	}
	if strings.HasPrefix(trimmed, "-") {
		return types.Raw{}, fmt.Errorf("price %q is negative", value)
	}

	whole, frac, hasFrac := strings.Cut(trimmed, ".")
	if whole == "" {
		whole = "0"
	}
	if hasFrac && frac == "" {
		return types.Raw{}, fmt.Errorf("price %q has a trailing separator", value)
	}

	scale := int(decimals)
	if len(frac) > scale {
		frac = frac[:scale] // truncate toward zero
	}
	padded := frac + strings.Repeat("0", scale-len(frac))

	digits := whole + padded
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return types.Raw{}, fmt.Errorf("price %q is not a decimal number", value)
	}
	return types.NewRaw(n), nil
}

// FormatScaled renders raw base units back to a decimal string. Used in logs and errors so an
// operator reads a price rather than a twenty-digit integer.
func FormatScaled(value types.Raw, decimals uint8) string {
	digits := value.Big().String()
	scale := int(decimals)

	if scale == 0 {
		return digits
	}
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}

	split := len(digits) - scale
	frac := strings.TrimRight(digits[split:], "0")
	if frac == "" {
		return digits[:split]
	}
	return digits[:split] + "." + frac
}
