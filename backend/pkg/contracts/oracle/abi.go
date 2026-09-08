// Package oracle holds the EVM ABIs for contracts/src/oracle.
//
// Regenerate with `make backend-abi` after any change to either contract's external surface.
// EVM-specific by definition; imported only from internal/chain/evm.
package oracle

import (
	_ "embed"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

//go:embed OracleRounds.abi.json
var roundsJSON string

//go:embed OracleStaking.abi.json
var stakingJSON string

// Event names the indexer consumes. Anything not listed here is emitted but not indexed.
const (
	EventFeedRegistered   = "FeedRegistered"
	EventFeedDeregistered = "FeedDeregistered"
	EventRoundStarted     = "RoundStarted"
	EventRoundQuorumMet   = "RoundQuorumMet"
	EventRoundSettled     = "RoundSettled"
	EventRoundFailed      = "RoundFailed"
	EventSubmission       = "SubmissionReceived"

	EventNodeRegistered  = "NodeRegistered"
	EventNodeStaked      = "NodeStaked"
	EventUnstakeRequest  = "UnstakeRequested"
	EventUnstakeCancel   = "UnstakeCancelled"
	EventNodeUnstaked    = "NodeUnstaked"
	EventNodeSlashed     = "NodeSlashed"
	EventNodeDeactivated = "NodeDeactivated"
	EventNodeReactivated = "NodeReactivated"
)

var (
	roundsABI  abi.ABI
	stakingABI abi.ABI
)

func init() {
	rounds, err := abi.JSON(strings.NewReader(roundsJSON))
	if err != nil {
		panic("oracle: embedded OracleRounds ABI is invalid: " + err.Error())
	}
	roundsABI = rounds

	staking, err := abi.JSON(strings.NewReader(stakingJSON))
	if err != nil {
		panic("oracle: embedded OracleStaking ABI is invalid: " + err.Error())
	}
	stakingABI = staking
}

// RoundsABI returns the parsed OracleRounds ABI.
func RoundsABI() abi.ABI { return roundsABI }

// StakingABI returns the parsed OracleStaking ABI.
func StakingABI() abi.ABI { return stakingABI }
