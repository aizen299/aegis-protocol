package types

import (
	"encoding/json"
	"math/big"
	"testing"
)

// A uint256 must survive the round trip through JSON and Postgres without becoming a float.
func TestRawSurvivesMaxUint256(t *testing.T) {
	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	raw := NewRaw(maxUint256)

	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded)[0] != '"' {
		t.Fatalf("encoded as %s; a uint256 must be a string, not a JSON number", encoded)
	}

	var decoded Raw
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.String() != maxUint256.String() {
		t.Fatalf("round trip lost precision:\n got %s\nwant %s", decoded.String(), maxUint256)
	}
}

func TestRawScanFromNumericText(t *testing.T) {
	var raw Raw
	if err := raw.Scan("340282366920938463463374607431768211456"); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if raw.String() != "340282366920938463463374607431768211456" {
		t.Fatalf("got %s", raw.String())
	}
}

func TestRawValueIsLosslessString(t *testing.T) {
	n, _ := new(big.Int).SetString("123456789012345678901234567890", 10)

	v, err := NewRaw(n).Value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	if v != "123456789012345678901234567890" {
		t.Fatalf("got %v", v)
	}
}

func TestRawZeroValueIsUsable(t *testing.T) {
	var raw Raw

	if raw.String() != "0" {
		t.Errorf("zero value renders as %q, want \"0\"", raw.String())
	}
	if !raw.IsZero() {
		t.Error("zero value should report IsZero")
	}
	if raw.Big().Sign() != 0 {
		t.Error("zero value should produce a zero big.Int, not nil")
	}
}

func TestRawRejectsNonInteger(t *testing.T) {
	for _, in := range []string{"1.5", "0x10", "abc", "1e18"} {
		if _, err := ParseRaw(in); err == nil {
			t.Errorf("ParseRaw(%q) should fail: raw amounts are integers in base units", in)
		}
	}
}

func TestRawSub(t *testing.T) {
	a := NewRaw(big.NewInt(1000))
	b := NewRaw(big.NewInt(250))

	if got := a.Sub(b).String(); got != "750" {
		t.Fatalf("got %s, want 750", got)
	}
}
