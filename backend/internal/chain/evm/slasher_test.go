package evm

import (
	"bytes"
	"testing"
)

// The reason reaches the contract as a right-padded bytes32 and comes back through the event the
// same way, so the round trip has to be exact or a penalty's reason changes between decision and
// audit trail.
func TestReasonBytes32RoundTripsThroughPadding(t *testing.T) {
	for _, reason := range []string{"OUTLIER_SUBMISSION", "MISSED_ROUND", "INVALID_SIGNATURE"} {
		padded := reasonBytes32(reason)

		trimmed := string(bytes.TrimRight(padded[:], "\x00"))
		if trimmed != reason {
			t.Errorf("reason %q became %q after padding and trimming", reason, trimmed)
		}
	}
}

// A reason longer than 32 bytes would be silently truncated on chain. Worth knowing the boundary
// rather than discovering it when a penalty is recorded under a mangled code.
func TestReasonBytes32TruncatesBeyondThirtyTwoBytes(t *testing.T) {
	long := "THIS_REASON_CODE_IS_DEFINITELY_LONGER_THAN_THIRTY_TWO_BYTES"

	padded := reasonBytes32(long)
	if string(padded[:]) != long[:32] {
		t.Fatalf("expected truncation to the first 32 bytes, got %q", padded)
	}
}

// The guard is detected by selector rather than message text, so it keeps working when node
// software changes how it renders a revert.
func TestAlreadySlashedSelectorIsStable(t *testing.T) {
	if len(alreadySlashedSelector) != 4 {
		t.Fatalf("selector is %d bytes", len(alreadySlashedSelector))
	}
	// keccak256("AlreadySlashedForRound(address,uint256)")[:4]
	if got := alreadySlashedSelector; got[0] == 0 && got[1] == 0 && got[2] == 0 && got[3] == 0 {
		t.Fatal("selector was not computed")
	}
}

type revertErr struct{ data string }

func (e revertErr) Error() string  { return "execution reverted" }
func (e revertErr) ErrorData() any { return e.data }

func TestAlreadySlashedIsDetectedFromRevertData(t *testing.T) {
	// Selector followed by two encoded words, as the node would return it.
	payload := "0x" + hexOf(alreadySlashedSelector) +
		"0000000000000000000000000000000000000000000000000000000000000001" +
		"0000000000000000000000000000000000000000000000000000000000000007"

	if !isAlreadySlashed(revertErr{data: payload}) {
		t.Fatal("the guard's revert was not recognised")
	}
}

func TestOtherRevertsAreNotMistakenForTheGuard(t *testing.T) {
	other := "0x" + "deadbeef" +
		"0000000000000000000000000000000000000000000000000000000000000001"

	if isAlreadySlashed(revertErr{data: other}) {
		t.Fatal("an unrelated revert was treated as the per-round guard")
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}
