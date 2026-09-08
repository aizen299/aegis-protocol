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
