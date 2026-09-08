package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Metadata is refreshed on conflict: a token can be redeployed behind the same address on a fresh
// local chain, and a stale decimals value would misinterpret every amount recorded against it.
const qUpsertAsset = `
	INSERT INTO assets (chain_id, address, decimals, symbol, name)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (chain_id, address)
	DO UPDATE SET decimals = EXCLUDED.decimals,
	              symbol   = EXCLUDED.symbol,
	              name     = EXCLUDED.name
`

const qGetAsset = `
	SELECT decimals, COALESCE(symbol, ''), COALESCE(name, '')
	FROM assets
	WHERE chain_id = $1 AND address = $2
`

func (s *Store) UpsertAsset(ctx context.Context, a types.AssetMetadata) error {
	_, err := s.pool.Exec(ctx, qUpsertAsset, a.ChainID, a.Address, int16(a.Decimals), nullable(a.Symbol), nullable(a.Name))
	if err != nil {
		return fmt.Errorf("upsert asset %d/%s: %w", a.ChainID, a.Address, err)
	}
	return nil
}

// Asset returns stored metadata, and false when the asset has not been seen.
func (s *Store) Asset(ctx context.Context, chainID int64, address string) (types.AssetMetadata, bool, error) {
	meta := types.AssetMetadata{ChainID: chainID, Address: address}

	var decimals int16
	err := s.pool.QueryRow(ctx, qGetAsset, chainID, address).Scan(&decimals, &meta.Symbol, &meta.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return meta, false, nil
	}
	if err != nil {
		return meta, false, fmt.Errorf("asset %d/%s: %w", chainID, address, err)
	}

	meta.Decimals = uint8(decimals)
	return meta, true, nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const qUpsertVault = `
	INSERT INTO vaults (chain_id, address, asset_address, share_offset)
	VALUES ($1, $2, $3, $4)
	ON CONFLICT (chain_id, address)
	DO UPDATE SET asset_address = EXCLUDED.asset_address, share_offset = EXCLUDED.share_offset
`

const qGetVault = `
	SELECT v.asset_address, v.share_offset, a.decimals
	FROM vaults v
	JOIN assets a ON a.chain_id = v.chain_id AND a.address = v.asset_address
	WHERE v.chain_id = $1 AND v.address = $2
`

func (s *Store) UpsertVault(ctx context.Context, v types.VaultMetadata) error {
	_, err := s.pool.Exec(ctx, qUpsertVault, v.ChainID, v.Address, v.AssetAddress, int16(v.ShareOffset))
	if err != nil {
		return fmt.Errorf("upsert vault %d/%s: %w", v.ChainID, v.Address, err)
	}
	return nil
}

// Vault returns stored metadata, and false when the vault has not been seen.
func (s *Store) Vault(ctx context.Context, chainID int64, address string) (types.VaultMetadata, bool, error) {
	meta := types.VaultMetadata{ChainID: chainID, Address: address}

	var offset, decimals int16
	err := s.pool.QueryRow(ctx, qGetVault, chainID, address).Scan(&meta.AssetAddress, &offset, &decimals)
	if errors.Is(err, pgx.ErrNoRows) {
		return meta, false, nil
	}
	if err != nil {
		return meta, false, fmt.Errorf("vault %d/%s: %w", chainID, address, err)
	}

	meta.ShareOffset = uint8(offset)
	meta.AssetDecimals = uint8(decimals)
	return meta, true, nil
}
