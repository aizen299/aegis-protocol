package types

import "time"

// Every oracle value below is raw and carries the scale that makes it readable, the same rule the
// vault types follow. A feed's scale comes from its registration rather than from a token, and a
// node's stake takes the scale of the stake asset.

// OracleFeed is a registered price or rate feed.
type OracleFeed struct {
	ChainID  int64  `json:"chainId"`
	FeedID   string `json:"feedId"`
	Name     string `json:"name"`
	Decimals uint8  `json:"decimals"`
	Active   bool   `json:"active"`
}

// OracleRound carries the eligibility snapshot the contract froze when the round opened.
// EligibleCount and NodeSetVersion are what quorum was judged against, not the live node set, so a
// settled round can be audited against the set that actually applied to it.
type OracleRound struct {
	ChainID         int64      `json:"chainId"`
	RoundID         Raw        `json:"roundId"`
	FeedID          string     `json:"feedId"`
	FeedName        string     `json:"feedName"`
	State           string     `json:"state"`
	OpenedAt        time.Time  `json:"openedAt"`
	Deadline        time.Time  `json:"deadline"`
	SettledAt       *time.Time `json:"settledAt,omitempty"`
	EligibleCount   int32      `json:"eligibleCount"`
	NodeSetVersion  Raw        `json:"nodeSetVersion"`
	SubmissionCount int32      `json:"submissionCount"`
	AggregatedValue Raw        `json:"aggregatedValue"`
	Decimals        uint8      `json:"decimals"`
	TxHash          string     `json:"txHash"`
	BlockNumber     uint64     `json:"blockNumber"`
}

// OracleSubmission is one node's reported value in a round.
type OracleSubmission struct {
	ChainID  int64  `json:"chainId"`
	RoundID  Raw    `json:"roundId"`
	Node     string `json:"node"`
	Value    Raw    `json:"value"`
	Decimals uint8  `json:"decimals"`
	Nonce    Raw    `json:"nonce"`
	// NonceKnown separates "no nonce recorded" from "the nonce was zero", which is a real value a
	// node's first submission carries.
	NonceKnown  bool      `json:"nonceKnown"`
	IsOutlier   bool      `json:"isOutlier"`
	TxHash      string    `json:"txHash"`
	LogIndex    uint      `json:"logIndex"`
	BlockNumber uint64    `json:"blockNumber"`
	SubmittedAt time.Time `json:"submittedAt"`
}

// OracleNode is a registered node's registry entry.
type OracleNode struct {
	ChainID        int64      `json:"chainId"`
	Address        string     `json:"address"`
	StakeAsset     string     `json:"stakeAsset"`
	StakedAmount   Raw        `json:"stakedAmount"`
	SlashedTotal   Raw        `json:"slashedTotal"`
	PendingUnstake Raw        `json:"pendingUnstake"`
	Decimals       uint8      `json:"decimals"`
	ClaimableAt    *time.Time `json:"claimableAt,omitempty"`
	MissedRounds   int32      `json:"missedRounds"`
	Active         bool       `json:"active"`
	RegisteredAt   time.Time  `json:"registeredAt"`
}
