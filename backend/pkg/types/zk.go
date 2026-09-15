package types

import "time"

// ZkGateMetadata is the wiring of a deployed gate, recorded so a reader can find the tree a proof
// must be built against without a contract call.
type ZkGateMetadata struct {
	ChainID         int64  `json:"chainId"`
	Address         string `json:"address"`
	TreeAddress     string `json:"tree"`
	VerifierAddress string `json:"verifier"`
}

// Commitment is one leaf of the on-chain tree.
//
// There is no owner field, and that is the design rather than an omission: the anonymity this
// module provides is the difficulty of linking a commitment to the action that spends it, and a
// column joining the two would hand that away. See migration 000008.
type Commitment struct {
	ChainID     int64     `json:"chainId"`
	TreeAddress string    `json:"tree"`
	LeafIndex   uint64    `json:"leafIndex"`
	Commitment  string    `json:"commitment"`
	RootAfter   string    `json:"rootAfter"`
	TxHash      string    `json:"txHash"`
	LogIndex    uint      `json:"logIndex"`
	BlockNumber uint64    `json:"blockNumber"`
	InsertedAt  time.Time `json:"insertedAt"`
}

// ZkAction is a domain the gate will accept proofs for.
type ZkAction struct {
	ChainID     int64  `json:"chainId"`
	GateAddress string `json:"gate"`
	ActionID    string `json:"actionId"`
	Name        string `json:"name"`
	Registered  bool   `json:"registered"`
	BlockNumber uint64 `json:"blockNumber"`
}

// PrivateAction is one spent nullifier. It records what the chain revealed and nothing more.
type PrivateAction struct {
	ChainID     int64     `json:"chainId"`
	GateAddress string    `json:"gate"`
	Nullifier   string    `json:"nullifier"`
	ActionID    string    `json:"actionId"`
	Root        string    `json:"root"`
	TxHash      string    `json:"txHash"`
	LogIndex    uint      `json:"logIndex"`
	BlockNumber uint64    `json:"blockNumber"`
	ExecutedAt  time.Time `json:"executedAt"`
}

// AnonymitySet is what bounds the privacy of a membership proof.
//
// docs/v0.4-zk-plan.md §4 records this as unresolved: with few commitments in the tree, "one of N"
// is weak, and at N=1 it is nothing. No circuit fixes that, so the size is served wherever a proof
// is offered rather than left for a caller to infer.
type AnonymitySet struct {
	ChainID     int64  `json:"chainId"`
	TreeAddress string `json:"tree"`
	LeafCount   uint64 `json:"leafCount"`
	CurrentRoot string `json:"currentRoot,omitempty"`
}
