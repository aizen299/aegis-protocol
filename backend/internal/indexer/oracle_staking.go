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
	SetOracleNodeUnstake(ctx context.Context, chainID int64, node string, pending types.Raw, claimableAt *time.Time) error
	SetOracleNodeActive(ctx context.Context, chainID int64, node string, active bool) error
	RecordOracleSlash(ctx context.Context, sl db.Slash) error
	OpenOracleNodeInterval(ctx context.Context, chainID int64, node string, version types.Raw, at time.Time) error
	CloseOracleNodeInterval(ctx context.Context, chainID int64, node string, version types.Raw, at time.Time, reason string) (bool, error)
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

// Handle records staking events. Whether a node is active is set only by NodeReactivated and
// NodeDeactivated, which the contract emits exactly when the set changes. An unstake request, cancel,
// or completion does not imply either: a cancel below the minimum stake, or into a full set, leaves the
// node inactive. ORC-2.
func (h *OracleStakingHandler) Handle(ctx context.Context, ev chain.Event) error {
	if ev.Contract != h.contract {
		return nil
	}
	// On Solana one program emits both staking and round events, so an event from this contract is not
	// necessarily one this handler owns.
	switch ev.Name {
	case eventNodeRegistered, eventNodeStaked, eventUnstakeRequest, eventUnstakeCancel,
		eventNodeUnstaked, eventNodeSlashed, eventNodeDeactivated, eventNodeReactivated:
	default:
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
		return h.store.SetOracleNodeUnstake(ctx, ev.ChainID, address, types.Raw{}, nil)
	case eventNodeUnstaked:
		remaining, err := rawField(ev, "remainingStake")
		if err != nil {
			return err
		}
		if err := h.store.SetOracleNodeUnstake(ctx, ev.ChainID, address, types.Raw{}, nil); err != nil {
			return err
		}
		return h.store.SetOracleNodeStake(ctx, ev.ChainID, address, remaining)
	case eventNodeSlashed:
		return h.handleSlashed(ctx, ev, address)
	case eventNodeDeactivated:
		return h.handleDeactivated(ctx, ev, address)
	case eventNodeReactivated:
		version, err := rawField(ev, "nodeSetVersion")
		if err != nil {
			return err
		}
		if err := h.store.OpenOracleNodeInterval(ctx, ev.ChainID, address, version, blockTime(ev)); err != nil {
			return err
		}
		return h.store.SetOracleNodeActive(ctx, ev.ChainID, address, true)
	default:
		return nil
	}
}

// handleDeactivated closes the node's current interval. An unmatched deactivation — its activation was
// never indexed, as happens when the indexer starts after the node joined — records nothing, so that
// period has no interval and the node is never judged for it. Unknown eligibility must not become a
// miss: that is the direction that slashes an honest node on incomplete data.
func (h *OracleStakingHandler) handleDeactivated(ctx context.Context, ev chain.Event, address string) error {
	version, err := rawField(ev, "nodeSetVersion")
	if err != nil {
		return err
	}
	reason, err := bytes32Field(ev, "reason")
	if err != nil {
		return err
	}
	if _, err := h.store.CloseOracleNodeInterval(ctx, ev.ChainID, address, version, blockTime(ev), trimBytes32(reason)); err != nil {
		return err
	}
	return h.store.SetOracleNodeActive(ctx, ev.ChainID, address, false)
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

	// The deactivation that accompanies a request arrives as its own NodeDeactivated event.
	return h.store.SetOracleNodeUnstake(ctx, ev.ChainID, address, amount, &claimableAt)
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
		TxHash:         ev.TxHash,
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
