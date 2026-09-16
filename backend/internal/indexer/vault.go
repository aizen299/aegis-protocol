package indexer

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/chain"
	"github.com/aizen299/aegis-protocol/backend/internal/db"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const (
	eventDeposited = "Deposited"
	eventWithdrawn = "Withdrawn"
)

// VaultStore is the write surface the vault handler needs.
type VaultStore interface {
	UpsertAsset(ctx context.Context, a types.AssetMetadata) error
	UpsertVault(ctx context.Context, v types.VaultMetadata) error
	InsertVaultDeposit(ctx context.Context, e db.VaultEvent) error
	InsertVaultWithdrawal(ctx context.Context, e db.VaultEvent) error
}

// AssetResolver reads token metadata from a chain.
type AssetResolver interface {
	TokenMetadata(ctx context.Context, token types.Identity) (chain.TokenMeta, error)
}

// VaultLocator says which vault an event belongs to, and reads that vault's asset and share offset.
//
// The emitter is not always the vault. On EVM each vault is its own contract; on Solana one program
// holds a vault per asset. Separate from chain.Client because a share offset is a property of this
// protocol's vault, not of a chain. See docs/v2.0-solana-plan.md §10.4.
type VaultLocator interface {
	LocateVault(ctx context.Context, emitter, asset types.Identity) (vault, vaultAsset types.Identity, shareOffset uint8, err error)
}

// VaultHandler turns VaultEngine events into rows. Writes use ON CONFLICT DO NOTHING keyed on
// (chain_id, tx_hash, log_index), so replaying a batch is a no-op.
//
// Amounts are persisted in raw base units exactly as emitted. The handler resolves the asset's
// decimals and records them alongside, rather than scaling on ingest — scaling here would bake one
// token's decimals into every row and misrepresent any asset that does not match.
type VaultHandler struct {
	contract types.Identity
	encode   func(types.Identity) string
	resolver AssetResolver
	locator  VaultLocator
	store    VaultStore
	chainID  int64

	mu     sync.Mutex
	assets map[types.Identity]types.AssetMetadata
	vaults map[types.Identity]types.Identity
}

func NewVaultHandler(store VaultStore, client chain.Client, locator VaultLocator, contract types.Identity) *VaultHandler {
	return &VaultHandler{
		contract: contract,
		encode:   client.EncodeIdentity,
		resolver: client,
		locator:  locator,
		store:    store,
		chainID:  client.ChainID(),
		assets:   make(map[types.Identity]types.AssetMetadata),
		vaults:   make(map[types.Identity]types.Identity),
	}
}

func (h *VaultHandler) Filters() []chain.Filter {
	return []chain.Filter{{
		Contract: h.contract,
		Names:    []string{eventDeposited, eventWithdrawn},
	}}
}

func (h *VaultHandler) Handle(ctx context.Context, ev chain.Event) error {
	if ev.Contract != h.contract {
		return nil
	}
	if ev.Name != eventDeposited && ev.Name != eventWithdrawn {
		return nil
	}

	asset, err := identityField(ev, "asset")
	if err != nil {
		return err
	}

	// Both metadata rows must exist before the event row. Foreign keys enforce it: no amount
	// without the decimals that scale it, and no share without the offset that scales it.
	if err := h.ensureAsset(ctx, asset); err != nil {
		return err
	}
	vault, err := h.ensureVault(ctx, ev.Contract, asset)
	if err != nil {
		return err
	}

	row, err := h.row(ev, vault, asset)
	if err != nil {
		return err
	}

	if ev.Name == eventDeposited {
		return h.store.InsertVaultDeposit(ctx, row)
	}
	return h.store.InsertVaultWithdrawal(ctx, row)
}

// ensureAsset resolves and persists token metadata once per asset per process. A token that does
// not expose decimals() is a hard failure: the batch is retried, and an operator seeds the row
// manually rather than the indexer guessing a scale.
func (h *VaultHandler) ensureAsset(ctx context.Context, asset types.Identity) error {
	h.mu.Lock()
	_, cached := h.assets[asset]
	h.mu.Unlock()
	if cached {
		return nil
	}

	meta, err := h.resolver.TokenMetadata(ctx, asset)
	if err != nil {
		return fmt.Errorf("resolve asset %s: %w", h.encode(asset), err)
	}

	record := types.AssetMetadata{
		ChainID:  h.chainID,
		Address:  h.encode(asset),
		Decimals: meta.Decimals,
		Symbol:   meta.Symbol,
		Name:     meta.Name,
	}
	if err := h.store.UpsertAsset(ctx, record); err != nil {
		return err
	}

	h.mu.Lock()
	h.assets[asset] = record
	h.mu.Unlock()
	return nil
}

// ensureVault locates the event's vault and persists its share offset once per asset per process.
// Like the asset path, a vault that cannot be read fails the batch rather than being given a default
// — a guessed offset misstates every share amount by a factor of ten to the guess.
func (h *VaultHandler) ensureVault(ctx context.Context, emitter, asset types.Identity) (types.Identity, error) {
	h.mu.Lock()
	vault, done := h.vaults[asset]
	h.mu.Unlock()
	if done {
		return vault, nil
	}

	vault, readAsset, offset, err := h.locator.LocateVault(ctx, emitter, asset)
	if err != nil {
		return types.Identity{}, fmt.Errorf("resolve vault for %s: %w", h.encode(asset), err)
	}
	// The event and the vault must agree on the asset; if they do not, one of them is being read
	// wrong and storing either would be a guess.
	if readAsset != asset {
		return types.Identity{}, fmt.Errorf("vault %s reports asset %s but the event names %s",
			h.encode(vault), h.encode(readAsset), h.encode(asset))
	}

	if err := h.store.UpsertVault(ctx, types.VaultMetadata{
		ChainID:      h.chainID,
		Address:      h.encode(vault),
		AssetAddress: h.encode(asset),
		ShareOffset:  offset,
	}); err != nil {
		return types.Identity{}, err
	}

	h.mu.Lock()
	h.vaults[asset] = vault
	h.mu.Unlock()
	return vault, nil
}

func (h *VaultHandler) row(ev chain.Event, vault, asset types.Identity) (db.VaultEvent, error) {
	user, err := identityField(ev, "user")
	if err != nil {
		return db.VaultEvent{}, err
	}
	amount, err := rawField(ev, "amount")
	if err != nil {
		return db.VaultEvent{}, err
	}
	shares, err := rawField(ev, "shares")
	if err != nil {
		return db.VaultEvent{}, err
	}

	return db.VaultEvent{
		ChainID:     ev.ChainID,
		User:        h.encode(user),
		Asset:       h.encode(asset),
		Vault:       h.encode(vault),
		Amount:      amount,
		Shares:      shares,
		TxHash:      ev.TxHash,
		LogIndex:    ev.LogIndex,
		BlockNumber: ev.BlockNumber,
		At:          time.Unix(ev.BlockTime, 0).UTC(),
	}, nil
}

func identityField(ev chain.Event, key string) (types.Identity, error) {
	raw, ok := ev.Payload[key]
	if !ok {
		return types.Identity{}, fmt.Errorf("event %s: missing field %q", ev.Name, key)
	}
	id, ok := raw.(types.Identity)
	if !ok {
		return types.Identity{}, fmt.Errorf("event %s: field %q is %T, want Identity", ev.Name, key, raw)
	}
	return id, nil
}

func rawField(ev chain.Event, key string) (types.Raw, error) {
	raw, ok := ev.Payload[key]
	if !ok {
		return types.Raw{}, fmt.Errorf("event %s: missing field %q", ev.Name, key)
	}
	n, ok := raw.(*big.Int)
	if !ok {
		return types.Raw{}, fmt.Errorf("event %s: field %q is %T, want *big.Int", ev.Name, key, raw)
	}
	if n.Sign() < 0 {
		return types.Raw{}, fmt.Errorf("event %s: field %q is negative", ev.Name, key)
	}
	return types.NewRaw(n), nil
}
