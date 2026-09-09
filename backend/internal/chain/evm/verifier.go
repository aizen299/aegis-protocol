package evm

import (
	"fmt"

	pbtypes "github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// SubmissionVerifier adapts EIP-712 verification to the interface the aggregation service depends
// on, keeping the service free of any EVM type.
type SubmissionVerifier struct {
	rounds pbtypes.Identity
}

func NewSubmissionVerifier(rounds pbtypes.Identity) *SubmissionVerifier {
	return &SubmissionVerifier{rounds: rounds}
}

// Verify checks a stored signature against the submission it claims to cover.
func (v *SubmissionVerifier) Verify(chainID int64, roundID pbtypes.Raw, feedID string, value pbtypes.Raw, node string, nonce pbtypes.Raw, signature []byte) (bool, error) {
	nodeID, err := pbtypes.IdentityFromEVMHex(node)
	if err != nil {
		return false, fmt.Errorf("node address %q: %w", node, err)
	}

	feed, err := feedIDFromHex(feedID)
	if err != nil {
		return false, err
	}

	return VerifySubmission(chainID, v.rounds, OracleSubmission{
		RoundID: roundID.Big(),
		FeedID:  feed,
		Value:   value.Big(),
		Node:    nodeID,
		Nonce:   nonce.Big(),
	}, signature)
}

// feedIDFromHex parses the 0x-prefixed 32-byte identifier the database stores.
func feedIDFromHex(s string) ([32]byte, error) {
	var out [32]byte

	trimmed := s
	if len(trimmed) >= 2 && (trimmed[:2] == "0x" || trimmed[:2] == "0X") {
		trimmed = trimmed[2:]
	}
	if len(trimmed) != 64 {
		return out, fmt.Errorf("feed id %q is not 32 bytes", s)
	}

	for i := 0; i < 32; i++ {
		var b byte
		if _, err := fmt.Sscanf(trimmed[i*2:i*2+2], "%02x", &b); err != nil {
			return out, fmt.Errorf("feed id %q: %w", s, err)
		}
		out[i] = b
	}
	return out, nil
}
