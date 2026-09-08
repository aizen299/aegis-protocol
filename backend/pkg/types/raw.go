package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math/big"
)

// Raw is an unscaled on-chain integer amount — a uint256 in the token's own base units.
//
// It exists so an amount cannot be accidentally treated as a scaled decimal. Conversion to a human
// figure requires the asset's decimals and happens at the presentation edge, never on ingest.
//
// It marshals to a JSON string, because a uint256 does not survive a float64.
type Raw struct {
	value *big.Int
}

func NewRaw(n *big.Int) Raw { return NewRawFromBig(n) }

// ParseRaw reads a base-10 integer string.
func ParseRaw(s string) (Raw, error) {
	if s == "" {
		return Raw{}, nil
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return Raw{}, fmt.Errorf("raw: %q is not a base-10 integer", s)
	}
	return Raw{value: n}, nil
}

func (r Raw) Big() *big.Int {
	if r.value == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(r.value)
}

func (r Raw) String() string {
	if r.value == nil {
		return "0"
	}
	return r.value.String()
}

func (r Raw) IsZero() bool { return r.value == nil || r.value.Sign() == 0 }

func (r Raw) Sub(other Raw) Raw {
	return Raw{value: new(big.Int).Sub(r.Big(), other.Big())}
}

func (r Raw) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.String())
}

func (r *Raw) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("raw: expected a decimal string: %w", err)
	}
	parsed, err := ParseRaw(s)
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}

// Value writes the integer to a NUMERIC(78,0) column. pgx sends it as a string so no precision is
// lost on the way to Postgres.
func (r Raw) Value() (driver.Value, error) { return r.String(), nil }

// Scan reads a NUMERIC(78,0) column.
func (r *Raw) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*r = Raw{}
		return nil
	case string:
		parsed, err := ParseRaw(v)
		if err != nil {
			return err
		}
		*r = parsed
		return nil
	case []byte:
		parsed, err := ParseRaw(string(v))
		if err != nil {
			return err
		}
		*r = parsed
		return nil
	case int64:
		*r = Raw{value: big.NewInt(v)}
		return nil
	default:
		return fmt.Errorf("raw: cannot scan %T", src)
	}
}
