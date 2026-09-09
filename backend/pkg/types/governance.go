package types

import "time"

// Proposal states, mirroring IGovernor.ProposalState. `dispatched` and `failed` are unreachable in
// Phase 1; they exist so that adding cross-chain execution later is not a migration of the state
// machine. See docs/project-spec.md §7.
const (
	ProposalStatePending    = "pending"
	ProposalStateActive     = "active"
	ProposalStateSucceeded  = "succeeded"
	ProposalStateDefeated   = "defeated"
	ProposalStateQueued     = "queued"
	ProposalStateDispatched = "dispatched"
	ProposalStateExecuted   = "executed"
	ProposalStateFailed     = "failed"
	ProposalStateCancelled  = "cancelled"
)

// Vote support values, mirroring IGovernor.Support.
const (
	SupportAgainst uint8 = 0
	SupportFor     uint8 = 1
	SupportAbstain uint8 = 2
)

// GovernorMetadata is what makes a raw vote weight interpretable, the way AssetMetadata does for a
// token amount. Weights are denominated in the governor's token, so the token is recorded and its
// decimals joined in rather than assumed.
type GovernorMetadata struct {
	ChainID         int64  `json:"chainId"`
	Address         string `json:"address"`
	TokenAddress    string `json:"token"`
	TimelockAddress string `json:"timelock"`
	TokenDecimals   uint8  `json:"tokenDecimals"`
}

// ProposalAction is the (chain, target, payload) triple a proposal executes.
//
// Target is 32 bytes rendered as hex, not an address: a Solana program is 32 bytes and §7 requires
// any identity crossing a layer boundary to be wide enough for one. TargetChainID equals ChainID
// for every proposal reachable in Phase 1.
type ProposalAction struct {
	TargetChainID int64  `json:"targetChainId"`
	Target        string `json:"target"`
	Value         Raw    `json:"value"`
	Calldata      string `json:"calldata"`
}

// Proposal is the indexed view of a governance proposal.
//
// VoteDecimals accompanies every weight for the same reason asset decimals accompany every amount:
// a raw value without its scale is uninterpretable, and the caller must never have to guess.
type Proposal struct {
	ChainID         int64          `json:"chainId"`
	GovernorAddress string         `json:"governor"`
	ProposalID      Raw            `json:"proposalId"`
	Proposer        string         `json:"proposer"`
	Title           string         `json:"title"`
	Description     string         `json:"description,omitempty"`
	Action          ProposalAction `json:"action"`
	State           string         `json:"state"`
	VoteStart       int64          `json:"voteStart"`
	VoteEnd         int64          `json:"voteEnd"`
	VotesFor        Raw            `json:"votesFor"`
	VotesAgainst    Raw            `json:"votesAgainst"`
	VotesAbstain    Raw            `json:"votesAbstain"`
	VoteDecimals    uint8          `json:"voteDecimals"`
	OperationID     *Raw           `json:"operationId,omitempty"`
	ExecutableAt    *time.Time     `json:"executableAt,omitempty"`
	QueuedAt        *time.Time     `json:"queuedAt,omitempty"`
	DispatchedAt    *time.Time     `json:"dispatchedAt,omitempty"`
	ExecutedAt      *time.Time     `json:"executedAt,omitempty"`
	CancelledAt     *time.Time     `json:"cancelledAt,omitempty"`
	TxHash          string         `json:"txHash"`
	LogIndex        uint           `json:"logIndex"`
	BlockNumber     uint64         `json:"blockNumber"`
}

// Vote is the indexed view of a cast vote.
type Vote struct {
	ChainID      int64     `json:"chainId"`
	ProposalID   Raw       `json:"proposalId"`
	Voter        string    `json:"voter"`
	Support      uint8     `json:"support"`
	Weight       Raw       `json:"weight"`
	VoteDecimals uint8     `json:"voteDecimals"`
	Reason       string    `json:"reason,omitempty"`
	TxHash       string    `json:"txHash"`
	LogIndex     uint      `json:"logIndex"`
	BlockNumber  uint64    `json:"blockNumber"`
	VotedAt      time.Time `json:"votedAt"`
}
