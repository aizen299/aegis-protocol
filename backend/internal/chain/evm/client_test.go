package evm

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// Postgres compares tx_hash as text, so a hash written in mixed case would be a different identity
// from the same hash in lower case, and replay would insert it twice.
func TestTxHashHexIsCanonical(t *testing.T) {
	h := common.HexToHash("0xDEADBEEF00000000000000000000000000000000000000000000000000000001")
	want := "0xdeadbeef00000000000000000000000000000000000000000000000000000001"
	if got := TxHashHex(h); got != want {
		t.Fatalf("TxHashHex = %s, want %s", got, want)
	}
}
