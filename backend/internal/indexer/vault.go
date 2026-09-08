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
	InsertVaultDeposit(ctx context.Context, e db.VaultEvent) error
	InsertVaultWithdrawal(ctx context.Context, e db.VaultEvent) error
}

// AssetResolver reads token metadata from a chain.
type AssetResolver interface {
	TokenMetadata(ctx context.Context, token types.Identity) (chain.TokenMeta, error)
}

// VaultHandler turns VaultEngine events into rows. Writes use ON CONFLICT DO NOTHING keyed on
// (chain_id, tx_hash, log_index), so replaying a batch is a no-op.
//
// Amounts are persisted in raw base units exactly as emitted. The handler resolves the asset's
// decimals and records them alongside, rather than scaling on ingest — scaling here would bake one
// token's decimals into every row and misrepresent any asset that does not match.
type VaultHandler struct {
	vault    types.Identity
	encode   func(types.Identity) string
	resolver AssetResolver
	store    VaultStore
	chainID  int64

	mu     sync.Mutex
	assets map[types.Identity]types.AssetMetadata
}

func NewVaultHandler(store VaultStore, client chain.Client, vault types.Identity) *VaultHandler {
	return &VaultHandler{
		vault:    vault,
		encode:   client.EncodeIdentity,
		resolver: client,
		store:    store,
		chainID:  client.ChainID(),
		assets:   make(map[types.Identity]types.AssetMetadata),
	}
}

func (h *VaultHandler) Filters() []chain.Filter {
	return []chain.Filter{{
		Contract: h.vault,
		Names:    []string{eventDeposited, eventWithdrawn},
	}}
}

func (h *VaultHandler) Handle(ctx context.Context, ev chain.Event) error {
	if ev.Contract != h.vault {
		return nil
	}
	if ev.Name != eventDeposited && ev.Name != eventWithdrawn {
		return nil
	}

	asset, err := identityField(ev, "asset")
	if err != nil {
		return err
	}

	// The asset row must exist before the event row: a foreign key enforces that no amount is
	// stored without the decimals needed to interpret it.
	if err := h.ensureAsset(ctx, asset); err != nil {
		return err
	}

	row, err := h.row(ev, asset)
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

func (h *VaultHandler) row(ev chain.Event, asset types.Identity) (db.VaultEvent, error) {
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
		Vault:       h.encode(ev.Contract),
		Amount:      amount,
		Shares:      shares,
		TxHash:      txHashHex(ev.TxHash),
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

func txHashHex(h [32]byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 2+len(h)*2)
	out[0], out[1] = '0', 'x'
	for i, b := range h {
		out[2+i*2] = hexDigits[b>>4]
		out[3+i*2] = hexDigits[b&0x0f]
	}
	return string(out)
}
