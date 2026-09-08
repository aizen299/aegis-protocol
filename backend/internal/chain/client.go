// Package chain defines the VM-agnostic view of a blockchain that every consumer depends on.
//
// Indexer, oracle, and vault packages import this package only. They must not import ethclient or
// any other VM-specific driver — an SVM implementation is added alongside evm/ in Phase 2 without
// touching them. See docs/project-spec.md §7.
package chain

import (
	"context"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Filter selects the events a caller wants from a range of blocks.
type Filter struct {
	Contract types.Identity
	// Names restricts the filter to these event names. Empty means all events on the contract.
	Names []string
}

// Event is a decoded, chain-neutral state change. Payload holds the decoded fields; the value
// types are the implementation's concern and are documented per event by the decoder that
// produced them.
type Event struct {
	ChainID     int64
	BlockNumber uint64
	BlockTime   int64
	TxHash      [32]byte
	LogIndex    uint
	Contract    types.Identity
	Name        string
	Payload     map[string]any
}

// TokenMeta is the on-chain metadata of a fungible token. Decimals are a property of the asset,
// never of the protocol: an amount in raw base units is uninterpretable without them.
type TokenMeta struct {
	Decimals uint8
	Symbol   string
	Name     string
}

// Client is the read surface of one chain. One Client serves one chain; a multi-chain deployment
// runs one indexer process per chain.
type Client interface {
	ChainID() int64

	// Head returns the current chain head. It is not necessarily confirmed.
	Head(ctx context.Context) (uint64, error)

	// ConfirmationDepth is how far behind the head a block must be before it is safe to process.
	// The finality rule lives here because it differs per chain: an EVM confirmation window is not
	// how Solana finality works.
	ConfirmationDepth() uint64

	// LogsInRange returns decoded events in [from, to] inclusive. Decoding is the implementation's
	// concern; callers receive normalised Events.
	LogsInRange(ctx context.Context, from, to uint64, filters []Filter) ([]Event, error)

	// EncodeIdentity renders an Identity in this chain's canonical string form. The result is what
	// is written to Postgres and returned by the API.
	EncodeIdentity(id types.Identity) string

	// DecodeIdentity parses this chain's canonical string form.
	DecodeIdentity(s string) (types.Identity, error)

	// TokenMetadata reads a fungible token's metadata. Chain-specific: an EVM token exposes it
	// through view calls, while an SPL mint carries decimals in its account data.
	TokenMetadata(ctx context.Context, token types.Identity) (TokenMeta, error)

	Close()
}
