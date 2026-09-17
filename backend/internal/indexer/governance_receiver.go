package indexer

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	eventMessageReceived  = "MessageReceived"
	eventMessageExecuted  = "MessageExecuted"
	eventMessageCancelled = "MessageCancelled"
)

// GovernanceReceiverStore is the write surface the receiver handler needs.
type GovernanceReceiverStore interface {
	InsertRemoteAction(ctx context.Context, a db.RemoteActionReceived) error
	CloseRemoteAction(ctx context.Context, chainID int64, emitterChain uint16, sequence types.Raw, status string, at time.Time, txHash, by string) error
}

// GovernanceReceiverHandler records governance messages received on this chain and what became of
// them. docs/v2.0-solana-plan.md §18.8.
type GovernanceReceiverHandler struct {
	contract types.Identity
	encode   func(types.Identity) string
	store    GovernanceReceiverStore
}

func NewGovernanceReceiverHandler(store GovernanceReceiverStore, client chain.Client, receiver types.Identity) *GovernanceReceiverHandler {
	return &GovernanceReceiverHandler{contract: receiver, encode: client.EncodeIdentity, store: store}
}

func (h *GovernanceReceiverHandler) Filters() []chain.Filter {
	return []chain.Filter{{
		Contract: h.contract,
		Names:    []string{eventMessageReceived, eventMessageExecuted, eventMessageCancelled},
	}}
}

func (h *GovernanceReceiverHandler) Handle(ctx context.Context, ev chain.Event) error {
	if ev.Contract != h.contract {
		return nil
	}
	switch ev.Name {
	case eventMessageReceived:
		return h.handleReceived(ctx, ev)
	case eventMessageExecuted:
		return h.handleClosed(ctx, ev, types.RemoteActionExecuted, "executor")
	case eventMessageCancelled:
		return h.handleClosed(ctx, ev, types.RemoteActionCancelled, "by")
	default:
		return nil
	}
}

func (h *GovernanceReceiverHandler) handleReceived(ctx context.Context, ev chain.Event) error {
	emitterChain, sequence, err := messageKey(ev)
	if err != nil {
		return err
	}
	source, err := int64Field(ev, "sourceChainId")
	if err != nil {
		return err
	}
	operationID, err := rawField(ev, "operationId")
	if err != nil {
		return err
	}
	target, err := identityField(ev, "target")
	if err != nil {
		return err
	}
	declared, err := bytes32Field(ev, "declaredValue")
	if err != nil {
		return err
	}
	accountsHash, err := bytes32Field(ev, "accountsHash")
	if err != nil {
		return err
	}
	executableAt, err := unixField(ev, "executableAt")
	if err != nil {
		return err
	}
	return h.store.InsertRemoteAction(ctx, db.RemoteActionReceived{
		ChainID:       ev.ChainID,
		Receiver:      h.encode(h.contract),
		EmitterChain:  emitterChain,
		Sequence:      sequence,
		SourceChainID: source,
		OperationID:   operationID,
		Target:        h.encode(target),
		DeclaredValue: types.NewRaw(new(big.Int).SetBytes(declared[:])),
		AccountsHash:  accountsHash,
		ExecutableAt:  executableAt,
		ReceivedAt:    blockTime(ev),
		TxHash:        ev.TxHash,
	})
}

func (h *GovernanceReceiverHandler) handleClosed(ctx context.Context, ev chain.Event, status, byField string) error {
	emitterChain, sequence, err := messageKey(ev)
	if err != nil {
		return err
	}
	by, err := identityField(ev, byField)
	if err != nil {
		return err
	}
	return h.store.CloseRemoteAction(ctx, ev.ChainID, emitterChain, sequence, status, blockTime(ev), ev.TxHash, h.encode(by))
}

func messageKey(ev chain.Event) (uint16, types.Raw, error) {
	emitterChain, err := rawField(ev, "emitterChain")
	if err != nil {
		return 0, types.Raw{}, err
	}
	if !emitterChain.Big().IsUint64() || emitterChain.Big().Uint64() > 0xffff {
		return 0, types.Raw{}, fmt.Errorf("event %s: emitterChain %s does not fit a u16", ev.Name, emitterChain)
	}
	sequence, err := rawField(ev, "sequence")
	if err != nil {
		return 0, types.Raw{}, err
	}
	return uint16(emitterChain.Big().Uint64()), sequence, nil
}

func int64Field(ev chain.Event, key string) (int64, error) {
	v, err := rawField(ev, key)
	if err != nil {
		return 0, err
	}
	if !v.Big().IsInt64() {
		return 0, fmt.Errorf("event %s: field %q = %s does not fit an int64", ev.Name, key, v)
	}
	return v.Big().Int64(), nil
}
