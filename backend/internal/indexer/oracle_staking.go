package indexer

import (
	"context"
	"sync"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// OracleNodeStore is the write surface the staking handler needs.
type OracleNodeStore interface {
	UpsertAsset(ctx context.Context, a types.AssetMetadata) error
	UpsertOracleNode(ctx context.Context, e db.NodeStakeEvent) error
	SetOracleNodeStake(ctx context.Context, chainID int64, node string, stake types.Raw) error
	SetOracleNodeUnstake(ctx context.Context, chainID int64, node string, pending types.Raw, claimableAt *time.Time, active bool) error
	SetOracleNodeActive(ctx context.Context, chainID int64, node string, active bool) error
	RecordOracleSlash(ctx context.Context, sl db.Slash) error
}

// OracleStakingHandler turns OracleStaking events into node registry rows.
//
// Stake is a token amount, so the stake asset's decimals are resolved and recorded exactly as the
// vault does for its own asset. The database enforces it: oracle_nodes carries a foreign key to
// assets, so a stake figure cannot be stored without the scale that makes it readable.
type OracleStakingHandler struct {
	contract   types.Identity
	stakeAsset types.Identity
	encode     func(types.Identity) string
	resolver   AssetResolver
	store      OracleNodeStore
	chainID    int64

	once       sync.Once
	assetErr   error
	assetSaved bool
}

func NewOracleStakingHandler(store OracleNodeStore, client chain.Client, contract, stakeAsset types.Identity) *OracleStakingHandler {
	return &OracleStakingHandler{
		contract:   contract,
		stakeAsset: stakeAsset,
		encode:     client.EncodeIdentity,
		resolver:   client,
		store:      store,
		chainID:    client.ChainID(),
	}
}

func (h *OracleStakingHandler) Filters() []chain.Filter {
	return []chain.Filter{{
		Contract: h.contract,
		Names: []string{
			eventNodeRegistered, eventNodeStaked, eventUnstakeRequest, eventUnstakeCancel,
			eventNodeUnstaked, eventNodeSlashed, eventNodeDeactivated, eventNodeReactivated,
		},
	}}
}

func (h *OracleStakingHandler) Handle(ctx context.Context, ev chain.Event) error {
	if ev.Contract != h.contract {
		return nil
	}

	node, err := identityField(ev, "node")
	if err != nil {
		return err
	}
	address := h.encode(node)

	switch ev.Name {
	case eventNodeRegistered:
		return h.handleRegistered(ctx, ev, address)
	case eventNodeStaked:
		total, err := rawField(ev, "totalStake")
		if err != nil {
			return err
		}
		return h.store.SetOracleNodeStake(ctx, ev.ChainID, address, total)
	case eventUnstakeRequest:
		return h.handleUnstakeRequested(ctx, ev, address)
	case eventUnstakeCancel:
		return h.store.SetOracleNodeUnstake(ctx, ev.ChainID, address, types.Raw{}, nil, true)
	case eventNodeUnstaked:
		remaining, err := rawField(ev, "remainingStake")
		if err != nil {
			return err
		}
		if err := h.store.SetOracleNodeUnstake(ctx, ev.ChainID, address, types.Raw{}, nil, false); err != nil {
			return err
		}
		return h.store.SetOracleNodeStake(ctx, ev.ChainID, address, remaining)
	case eventNodeSlashed:
		return h.handleSlashed(ctx, ev, address)
	case eventNodeDeactivated:
		return h.store.SetOracleNodeActive(ctx, ev.ChainID, address, false)
	case eventNodeReactivated:
		return h.store.SetOracleNodeActive(ctx, ev.ChainID, address, true)
	default:
		return nil
	}
}

func (h *OracleStakingHandler) handleRegistered(ctx context.Context, ev chain.Event, address string) error {
	stake, err := rawField(ev, "stake")
	if err != nil {
		return err
	}
	if err := h.ensureStakeAsset(ctx); err != nil {
		return err
	}

	return h.store.UpsertOracleNode(ctx, db.NodeStakeEvent{
		ChainID:      ev.ChainID,
		Address:      address,
		StakeAsset:   h.encode(h.stakeAsset),
		StakedAmount: stake,
		At:           blockTime(ev),
	})
}

func (h *OracleStakingHandler) handleUnstakeRequested(ctx context.Context, ev chain.Event, address string) error {
	amount, err := rawField(ev, "amount")
	if err != nil {
		return err
	}
	claimableAt, err := unixField(ev, "claimableAt")
	if err != nil {
		return err
	}

	// Requesting an exit deactivates the node on-chain, so the registry must reflect that or an
	// aggregation service would count a departing node toward quorum.
	return h.store.SetOracleNodeUnstake(ctx, ev.ChainID, address, amount, &claimableAt, false)
}

func (h *OracleStakingHandler) handleSlashed(ctx context.Context, ev chain.Event, address string) error {
	amount, err := rawField(ev, "amount")
	if err != nil {
		return err
	}
	remaining, err := rawField(ev, "remainingStake")
	if err != nil {
		return err
	}
	reason, err := bytes32Field(ev, "reason")
	if err != nil {
		return err
	}
	roundID, err := rawField(ev, "roundId")
	if err != nil {
		return err
	}

	return h.store.RecordOracleSlash(ctx, db.Slash{
		ChainID:        ev.ChainID,
		Node:           address,
		RoundID:        roundID,
		Amount:         amount,
		RemainingStake: remaining,
		Reason:         trimBytes32(reason),
		TxHash:         txHashHex(ev.TxHash),
		LogIndex:       ev.LogIndex,
		BlockNumber:    ev.BlockNumber,
		At:             blockTime(ev),
	})
}

// ensureStakeAsset resolves the stake token's decimals once per process. A token that does not
// expose decimals() fails the batch rather than defaulting, the same rule the vault applies.
func (h *OracleStakingHandler) ensureStakeAsset(ctx context.Context) error {
	h.once.Do(func() {
		meta, err := h.resolver.TokenMetadata(ctx, h.stakeAsset)
		if err != nil {
			h.assetErr = err
			return
		}
		h.assetErr = h.store.UpsertAsset(ctx, types.AssetMetadata{
			ChainID:  h.chainID,
			Address:  h.encode(h.stakeAsset),
			Decimals: meta.Decimals,
			Symbol:   meta.Symbol,
			Name:     meta.Name,
		})
		h.assetSaved = h.assetErr == nil
	})

	if h.assetErr != nil {
		// sync.Once has already fired, so a transient RPC failure would otherwise poison every
		// later batch. Reset so the next batch retries.
		h.once = sync.Once{}
		err := h.assetErr
		h.assetErr = nil
		return err
	}
	return nil
}
