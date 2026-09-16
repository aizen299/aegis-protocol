package indexer

import (
	"context"
	"fmt"
	"sync"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	eventCommitmentInserted    = "CommitmentInserted"
	eventPrivateActionExecuted = "PrivateActionExecuted"
	eventActionRegistered      = "ActionRegistered"
	eventActionDeregistered    = "ActionDeregistered"
)

// ZkStore is the write surface the zk handler needs.
type ZkStore interface {
	UpsertZkGate(ctx context.Context, g types.ZkGateMetadata) error
	InsertZkCommitment(ctx context.Context, c db.ZkCommitment) error
	UpsertZkAction(ctx context.Context, a db.ZkActionRow) error
	DeregisterZkAction(ctx context.Context, chainID int64, gate, actionID string) error
	InsertZkPrivateAction(ctx context.Context, a db.ZkPrivateAction) error
}

// ZkGateMetadataReader reads a gate's tree and verifier.
//
// Narrow and separate from chain.Client for the same reason the vault and governor readers are:
// these are properties of this protocol's contracts, not chain-generic concepts.
type ZkGateMetadataReader interface {
	ZkGateMetadata(ctx context.Context, gate types.Identity) (tree, verifier types.Identity, err error)
}

// ZkHandler turns CommitmentTree and ZkVaultGate events into rows.
//
// It mirrors the tree: §2.2 puts the tree on chain and gives the indexer the job of rebuilding it,
// because a wrong mirror is a liveness bug — proofs stop verifying — while a wrong posted root
// would forge membership. The mirror is therefore derived from logs and never asserted.
//
// It records no sender for anything. That is the privacy property, argued in migration 000008.
type ZkHandler struct {
	tree     types.Identity
	gate     types.Identity
	encode   func(types.Identity) string
	gateMeta ZkGateMetadataReader
	store    ZkStore
	chainID  int64

	mu      sync.Mutex
	gateSet bool
}

func NewZkHandler(store ZkStore, client chain.Client, gateMeta ZkGateMetadataReader, tree, gate types.Identity) *ZkHandler {
	return &ZkHandler{
		tree:     tree,
		gate:     gate,
		encode:   client.EncodeIdentity,
		gateMeta: gateMeta,
		store:    store,
		chainID:  client.ChainID(),
	}
}

func (h *ZkHandler) Filters() []chain.Filter {
	return []chain.Filter{
		{Contract: h.tree, Names: []string{eventCommitmentInserted}},
		{
			Contract: h.gate,
			Names: []string{
				eventPrivateActionExecuted, eventActionRegistered, eventActionDeregistered,
			},
		},
	}
}

func (h *ZkHandler) Handle(ctx context.Context, ev chain.Event) error {
	switch {
	case ev.Contract == h.tree && ev.Name == eventCommitmentInserted:
		return h.handleCommitmentInserted(ctx, ev)
	case ev.Contract == h.gate && ev.Name == eventPrivateActionExecuted:
		return h.handlePrivateAction(ctx, ev)
	case ev.Contract == h.gate && ev.Name == eventActionRegistered:
		return h.handleActionRegistered(ctx, ev)
	case ev.Contract == h.gate && ev.Name == eventActionDeregistered:
		return h.handleActionDeregistered(ctx, ev)
	default:
		return nil
	}
}

// ensureGate records the gate's wiring before any row referencing it is written. The actions and
// private-actions tables have a foreign key to it, so a gate that cannot be read fails the batch
// rather than producing rows nothing can interpret.
func (h *ZkHandler) ensureGate(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.gateSet {
		return nil
	}

	tree, verifier, err := h.gateMeta.ZkGateMetadata(ctx, h.gate)
	if err != nil {
		return fmt.Errorf("read zk gate metadata: %w", err)
	}

	if err := h.store.UpsertZkGate(ctx, types.ZkGateMetadata{
		ChainID:         h.chainID,
		Address:         h.encode(h.gate),
		TreeAddress:     h.encode(tree),
		VerifierAddress: h.encode(verifier),
	}); err != nil {
		return err
	}

	h.gateSet = true
	return nil
}

func (h *ZkHandler) handleCommitmentInserted(ctx context.Context, ev chain.Event) error {
	leafIndex, err := rawField(ev, "leafIndex")
	if err != nil {
		return err
	}
	commitment, err := bytes32Field(ev, "commitment")
	if err != nil {
		return err
	}
	root, err := bytes32Field(ev, "root")
	if err != nil {
		return err
	}
	insertedAt, err := unixField(ev, "timestamp")
	if err != nil {
		return err
	}

	return h.store.InsertZkCommitment(ctx, db.ZkCommitment{
		ChainID:     ev.ChainID,
		TreeAddress: h.encode(h.tree),
		LeafIndex:   leafIndex.Big().Uint64(),
		Commitment:  hexBytes32(commitment),
		RootAfter:   hexBytes32(root),
		TxHash:      ev.TxHash,
		LogIndex:    ev.LogIndex,
		BlockNumber: ev.BlockNumber,
		InsertedAt:  insertedAt,
	})
}

func (h *ZkHandler) handlePrivateAction(ctx context.Context, ev chain.Event) error {
	if err := h.ensureGate(ctx); err != nil {
		return err
	}

	nullifier, err := bytes32Field(ev, "nullifier")
	if err != nil {
		return err
	}
	actionID, err := bytes32Field(ev, "actionId")
	if err != nil {
		return err
	}
	root, err := bytes32Field(ev, "root")
	if err != nil {
		return err
	}

	return h.store.InsertZkPrivateAction(ctx, db.ZkPrivateAction{
		ChainID:     ev.ChainID,
		GateAddress: h.encode(h.gate),
		Nullifier:   hexBytes32(nullifier),
		ActionID:    hexBytes32(actionID),
		Root:        hexBytes32(root),
		TxHash:      ev.TxHash,
		LogIndex:    ev.LogIndex,
		BlockNumber: ev.BlockNumber,
		ExecutedAt:  blockTime(ev),
	})
}

func (h *ZkHandler) handleActionRegistered(ctx context.Context, ev chain.Event) error {
	if err := h.ensureGate(ctx); err != nil {
		return err
	}

	actionID, err := bytes32Field(ev, "actionId")
	if err != nil {
		return err
	}
	name, err := stringField(ev, "name")
	if err != nil {
		return err
	}

	return h.store.UpsertZkAction(ctx, db.ZkActionRow{
		ChainID:     ev.ChainID,
		GateAddress: h.encode(h.gate),
		ActionID:    hexBytes32(actionID),
		Name:        name,
		TxHash:      ev.TxHash,
		LogIndex:    ev.LogIndex,
		BlockNumber: ev.BlockNumber,
	})
}

func (h *ZkHandler) handleActionDeregistered(ctx context.Context, ev chain.Event) error {
	actionID, err := bytes32Field(ev, "actionId")
	if err != nil {
		return err
	}
	return h.store.DeregisterZkAction(ctx, ev.ChainID, h.encode(h.gate), hexBytes32(actionID))
}
