// Package governance holds the EVM ABI for contracts/src/governance/Governor.sol.
//
// Regenerate with `make backend-abi` after any change to the contract's external surface.
// EVM-specific by definition; imported only from internal/chain/evm.
package governance

import (
	_ "embed"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

//go:embed Governor.abi.json
var governorJSON string

// Event names the indexer consumes. Anything not listed here is emitted but not indexed.
const (
	EventProposalCreated    = "ProposalCreated"
	EventVoteCast           = "VoteCast"
	EventProposalQueued     = "ProposalQueued"
	EventProposalExecuted   = "ProposalExecuted"
	EventProposalDispatched = "ProposalDispatched"
	EventProposalCancelled  = "ProposalCancelled"
)

var governorABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(governorJSON))
	if err != nil {
		panic("governance: embedded Governor ABI is invalid: " + err.Error())
	}
	governorABI = parsed
}

// GovernorABI returns the parsed Governor ABI.
func GovernorABI() abi.ABI { return governorABI }
