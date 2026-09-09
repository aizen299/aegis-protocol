package indexer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	eventProposalCreated    = "ProposalCreated"
	eventVoteCast           = "VoteCast"
	eventProposalQueued     = "ProposalQueued"
	eventProposalExecuted   = "ProposalExecuted"
	eventProposalDispatched = "ProposalDispatched"
	eventProposalCancelled  = "ProposalCancelled"
)

// GovernanceStore is the write surface the governance handler needs.
type GovernanceStore interface {
	UpsertAsset(ctx context.Context, a types.AssetMetadata) error
	UpsertGovernor(ctx context.Context, g types.GovernorMetadata) error
	InsertGovernanceProposal(ctx context.Context, p db.GovernanceProposal) error
	InsertGovernanceVote(ctx context.Context, v db.GovernanceVote) error
	MarkProposalQueued(ctx context.Context, chainID int64, proposalID, operationID types.Raw, executableAt, at time.Time) error
	MarkProposalExecuted(ctx context.Context, chainID int64, proposalID types.Raw, at time.Time) error
	MarkProposalDispatched(ctx context.Context, chainID int64, proposalID types.Raw, at time.Time) error
	MarkProposalCancelled(ctx context.Context, chainID int64, proposalID types.Raw, at time.Time) error
}

// GovernorMetadataReader reads a governor's vote token and timelock.
//
// Narrow and separate from chain.Client for the same reason VaultMetadataReader is: these are
// properties of this protocol's contract, not chain-generic concepts.
type GovernorMetadataReader interface {
	GovernorMetadata(ctx context.Context, governor types.Identity) (token, timelock types.Identity, err error)
}

// GovernanceHandler turns Governor events into rows.
//
// Vote weights are persisted in raw base units exactly as emitted. The scale comes from the
// governor's token, resolved once and recorded, so a weight is never stored without the metadata
// that makes it readable.
type GovernanceHandler struct {
	contract     types.Identity
	encode       func(types.Identity) string
	resolver     AssetResolver
	governorMeta GovernorMetadataReader
	store        GovernanceStore
	chainID      int64

	mu          sync.Mutex
	governorSet bool
}

func NewGovernanceHandler(store GovernanceStore, client chain.Client, governorMeta GovernorMetadataReader, governor types.Identity) *GovernanceHandler {
	return &GovernanceHandler{
		contract:     governor,
		encode:       client.EncodeIdentity,
		resolver:     client,
		governorMeta: governorMeta,
		store:        store,
		chainID:      client.ChainID(),
	}
}

func (h *GovernanceHandler) Filters() []chain.Filter {
	return []chain.Filter{{
		Contract: h.contract,
		Names: []string{
			eventProposalCreated, eventVoteCast, eventProposalQueued,
			eventProposalExecuted, eventProposalDispatched, eventProposalCancelled,
		},
	}}
}

func (h *GovernanceHandler) Handle(ctx context.Context, ev chain.Event) error {
	if ev.Contract != h.contract {
		return nil
	}

	switch ev.Name {
	case eventProposalCreated:
		return h.handleProposalCreated(ctx, ev)
	case eventVoteCast:
		return h.handleVoteCast(ctx, ev)
	case eventProposalQueued:
		return h.handleQueued(ctx, ev)
	case eventProposalExecuted:
		return h.handleExecuted(ctx, ev)
	case eventProposalDispatched:
		return h.handleDispatched(ctx, ev)
	case eventProposalCancelled:
		return h.handleCancelled(ctx, ev)
	default:
		return nil
	}
}

// ensureGovernor records the governor and its vote token before any row referencing them is
// written. The proposals table has a foreign key to governors, and governors to assets, so a
// missing scale fails the batch rather than producing an uninterpretable weight.
func (h *GovernanceHandler) ensureGovernor(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.governorSet {
		return nil
	}

	token, timelock, err := h.governorMeta.GovernorMetadata(ctx, h.contract)
	if err != nil {
		return fmt.Errorf("read governor metadata: %w", err)
	}

	meta, err := h.resolver.TokenMetadata(ctx, token)
	if err != nil {
		return fmt.Errorf("read vote token metadata: %w", err)
	}

	asset := types.AssetMetadata{
		ChainID:  h.chainID,
		Address:  h.encode(token),
		Decimals: meta.Decimals,
		Symbol:   meta.Symbol,
		Name:     meta.Name,
	}
	if err := h.store.UpsertAsset(ctx, asset); err != nil {
		return err
	}

	if err := h.store.UpsertGovernor(ctx, types.GovernorMetadata{
		ChainID:         h.chainID,
		Address:         h.encode(h.contract),
		TokenAddress:    asset.Address,
		TimelockAddress: h.encode(timelock),
	}); err != nil {
		return err
	}

	h.governorSet = true
	return nil
}

func (h *GovernanceHandler) handleProposalCreated(ctx context.Context, ev chain.Event) error {
	if err := h.ensureGovernor(ctx); err != nil {
		return err
	}

	proposalID, err := rawField(ev, "proposalId")
	if err != nil {
		return err
	}
	proposer, err := identityField(ev, "proposer")
	if err != nil {
		return err
	}
	targetChainID, err := rawField(ev, "targetChainId")
	if err != nil {
		return err
	}
	target, err := bytes32Field(ev, "target")
	if err != nil {
		return err
	}
	value, err := rawField(ev, "value")
	if err != nil {
		return err
	}
	payload, err := bytesField(ev, "payload")
	if err != nil {
		return err
	}
	voteStart, err := rawField(ev, "voteStart")
	if err != nil {
		return err
	}
	voteEnd, err := rawField(ev, "voteEnd")
	if err != nil {
		return err
	}
	title, err := stringField(ev, "title")
	if err != nil {
		return err
	}
	description, err := stringField(ev, "description")
	if err != nil {
		return err
	}

	return h.store.InsertGovernanceProposal(ctx, db.GovernanceProposal{
		ChainID:         ev.ChainID,
		GovernorAddress: h.encode(h.contract),
		ProposalID:      proposalID,
		Proposer:        h.encode(proposer),
		Title:           title,
		Description:     description,
		// Stored as the 32 bytes the contract emitted, never narrowed to an address. A remote
		// target does not fit one, and the schema must not assume a local destination either.
		Target:        hexBytes32(target),
		TargetChainID: targetChainID.Big().Int64(),
		ActionValue:   value,
		Calldata:      payload,
		VoteStart:     voteStart.Big().Int64(),
		VoteEnd:       voteEnd.Big().Int64(),
		TxHash:        txHashHex(ev.TxHash),
		LogIndex:      ev.LogIndex,
		BlockNumber:   ev.BlockNumber,
	})
}

func (h *GovernanceHandler) handleVoteCast(ctx context.Context, ev chain.Event) error {
	proposalID, err := rawField(ev, "proposalId")
	if err != nil {
		return err
	}
	voter, err := identityField(ev, "voter")
	if err != nil {
		return err
	}
	support, err := uint8Field(ev, "support")
	if err != nil {
		return err
	}
	weight, err := rawField(ev, "weight")
	if err != nil {
		return err
	}
	reason, err := stringField(ev, "reason")
	if err != nil {
		return err
	}

	return h.store.InsertGovernanceVote(ctx, db.GovernanceVote{
		ChainID:     ev.ChainID,
		ProposalID:  proposalID,
		Voter:       h.encode(voter),
		Support:     support,
		Weight:      weight,
		Reason:      reason,
		TxHash:      txHashHex(ev.TxHash),
		LogIndex:    ev.LogIndex,
		BlockNumber: ev.BlockNumber,
		VotedAt:     blockTime(ev),
	})
}

func (h *GovernanceHandler) handleQueued(ctx context.Context, ev chain.Event) error {
	proposalID, err := rawField(ev, "proposalId")
	if err != nil {
		return err
	}
	operationID, err := rawField(ev, "operationId")
	if err != nil {
		return err
	}
	executableAt, err := unixField(ev, "executableAt")
	if err != nil {
		return err
	}

	return h.store.MarkProposalQueued(ctx, ev.ChainID, proposalID, operationID, executableAt, blockTime(ev))
}

func (h *GovernanceHandler) handleExecuted(ctx context.Context, ev chain.Event) error {
	proposalID, err := rawField(ev, "proposalId")
	if err != nil {
		return err
	}
	return h.store.MarkProposalExecuted(ctx, ev.ChainID, proposalID, blockTime(ev))
}

// Unreachable in Phase 1. Handled rather than ignored so the state is recorded the day a
// destination chain exists, instead of arriving as an unhandled event in production.
func (h *GovernanceHandler) handleDispatched(ctx context.Context, ev chain.Event) error {
	proposalID, err := rawField(ev, "proposalId")
	if err != nil {
		return err
	}
	return h.store.MarkProposalDispatched(ctx, ev.ChainID, proposalID, blockTime(ev))
}

func (h *GovernanceHandler) handleCancelled(ctx context.Context, ev chain.Event) error {
	proposalID, err := rawField(ev, "proposalId")
	if err != nil {
		return err
	}
	return h.store.MarkProposalCancelled(ctx, ev.ChainID, proposalID, blockTime(ev))
}
