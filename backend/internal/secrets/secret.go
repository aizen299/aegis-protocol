// Package secrets resolves sensitive material under a policy that cannot silently degrade.
//
// The policy exists because the aggregation service is the first component holding a key that can
// move value. See docs/v0.2-oracle-plan.md §2.7.
package secrets

import (
	"encoding/json"
	"fmt"
)

// Secret wraps sensitive material so it cannot reach a log by accident.
//
// String, GoString, and MarshalJSON all redact, which covers %s, %v, %#q, zerolog's reflection,
// and encoding/json. Reading the material requires calling Expose, which is greppable — an audit
// can enumerate every place the real value is touched.
type Secret struct {
	value string
}

func New(value string) Secret { return Secret{value: value} }

// Expose returns the material. Every call site is a place the secret leaves this package.
func (s Secret) Expose() string { return s.value }

func (s Secret) IsZero() bool { return s.value == "" }

func (s Secret) String() string { return "[redacted]" }

func (s Secret) GoString() string { return "secrets.Secret{[redacted]}" }

func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal("[redacted]") }

// Format catches %v, %s, %q and the rest. fmt checks Formatter before Stringer, so this is the
// backstop for verbs Stringer alone would not cover.
func (s Secret) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte("[redacted]"))
}
