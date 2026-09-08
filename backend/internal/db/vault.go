package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const qInsertDeposit = `
	INSERT INTO vault_deposits
		(chain_id, user_address, asset_address, vault_address, amount, shares, tx_hash, log_index, block_number, deposited_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (chain_id, tx_hash, log_index) DO NOTHING
`

const qInsertWithdrawal = `
	INSERT INTO vault_withdrawals
		(chain_id, user_address, asset_address, vault_address, amount, shares, tx_hash, log_index, block_number, withdrawn_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (chain_id, tx_hash, log_index) DO NOTHING
`

// Aggregation is pushed to Postgres rather than done in Go: the row counts are unbounded and the
// indexes are already chain-scoped. Sums stay in raw base units; the asset's decimals are joined in
// so the caller can scale once, at the edge.
const qVaultPosition = `
	SELECT
		COALESCE(d.shares, 0) - COALESCE(w.shares, 0) AS shares,
		COALESCE(d.amount, 0)                         AS deposited_total,
		COALESCE(w.amount, 0)                         AS withdrawn_total,
		COALESCE(d.asset, w.asset, '')                AS asset_address,
		COALESCE(a.decimals, 0)                       AS decimals,
		d.last_at
	FROM (
		SELECT SUM(shares) AS shares, SUM(amount) AS amount,
		       MIN(asset_address) AS asset, MAX(deposited_at) AS last_at
		FROM vault_deposits WHERE chain_id = $1 AND user_address = $2
	) d
	FULL OUTER JOIN (
		SELECT SUM(shares) AS shares, SUM(amount) AS amount, MIN(asset_address) AS asset
		FROM vault_withdrawals WHERE chain_id = $1 AND user_address = $2
	) w ON TRUE
	LEFT JOIN assets a ON a.chain_id = $1 AND a.address = COALESCE(d.asset, w.asset)
`

const qListDeposits = `
	SELECT d.user_address, d.asset_address, d.vault_address, d.amount, d.shares, a.decimals,
	       d.tx_hash, d.log_index, d.block_number, d.deposited_at
	FROM vault_deposits d
	JOIN assets a ON a.chain_id = d.chain_id AND a.address = d.asset_address
	WHERE d.chain_id = $1 AND d.user_address = $2
	ORDER BY d.block_number DESC, d.log_index DESC
	LIMIT $3 OFFSET $4
`

const qVaultTVL = `
	SELECT COALESCE(SUM(flows.amount), 0), COALESCE(MAX(flows.decimals), 0), COALESCE(MIN(flows.asset), '')
	FROM (
		SELECT d.amount, a.decimals, d.asset_address AS asset
		FROM vault_deposits d
		JOIN assets a ON a.chain_id = d.chain_id AND a.address = d.asset_address
		WHERE d.chain_id = $1 AND d.vault_address = $2
		UNION ALL
		SELECT -w.amount, a.decimals, w.asset_address
		FROM vault_withdrawals w
		JOIN assets a ON a.chain_id = w.chain_id AND a.address = w.asset_address
		WHERE w.chain_id = $1 AND w.vault_address = $2
	) flows
`

// VaultEvent is a row awaiting insertion. Amounts are raw base units, unscaled.
type VaultEvent struct {
	ChainID     int64
	User        string
	Asset       string
	Vault       string
	Amount      types.Raw
	Shares      types.Raw
	TxHash      string
	LogIndex    uint
	BlockNumber uint64
	At          time.Time
}

func (s *Store) InsertVaultDeposit(ctx context.Context, e VaultEvent) error {
	_, err := s.pool.Exec(ctx, qInsertDeposit,
		e.ChainID, e.User, e.Asset, e.Vault, e.Amount, e.Shares, e.TxHash, int32(e.LogIndex), int64(e.BlockNumber), e.At)
	if err != nil {
		return fmt.Errorf("insert deposit %s#%d: %w", e.TxHash, e.LogIndex, err)
	}
	return nil
}

func (s *Store) InsertVaultWithdrawal(ctx context.Context, e VaultEvent) error {
	_, err := s.pool.Exec(ctx, qInsertWithdrawal,
		e.ChainID, e.User, e.Asset, e.Vault, e.Amount, e.Shares, e.TxHash, int32(e.LogIndex), int64(e.BlockNumber), e.At)
	if err != nil {
		return fmt.Errorf("insert withdrawal %s#%d: %w", e.TxHash, e.LogIndex, err)
	}
	return nil
}

func (s *Store) VaultPosition(ctx context.Context, chainID int64, user string) (types.VaultPosition, error) {
	pos := types.VaultPosition{ChainID: chainID, UserAddress: user}

	var (
		decimals int16
		lastAt   *time.Time
	)
	err := s.pool.QueryRow(ctx, qVaultPosition, chainID, user).
		Scan(&pos.Shares, &pos.DepositedTotal, &pos.WithdrawnTotal, &pos.AssetAddress, &decimals, &lastAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return pos, nil
	}
	if err != nil {
		return pos, fmt.Errorf("vault position %d/%s: %w", chainID, user, err)
	}

	pos.Decimals = uint8(decimals)
	pos.LastDepositAt = lastAt
	return pos, nil
}

func (s *Store) ListVaultDeposits(ctx context.Context, chainID int64, user string, limit, offset int) ([]types.VaultDeposit, error) {
	rows, err := s.pool.Query(ctx, qListDeposits, chainID, user, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list deposits %d/%s: %w", chainID, user, err)
	}
	defer rows.Close()

	out := make([]types.VaultDeposit, 0, limit)
	for rows.Next() {
		d := types.VaultDeposit{ChainID: chainID}
		var decimals int16
		if err := rows.Scan(&d.UserAddress, &d.AssetAddress, &d.VaultAddress, &d.Amount, &d.Shares,
			&decimals, &d.TxHash, &d.LogIndex, &d.BlockNumber, &d.DepositedAt); err != nil {
			return nil, fmt.Errorf("scan deposit: %w", err)
		}
		d.Decimals = uint8(decimals)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deposits: %w", err)
	}
	return out, nil
}

// VaultTVL is derived from indexed flows in raw base units, not read from chain. It lags the chain
// by the confirmation window and is not authoritative for any on-chain decision.
func (s *Store) VaultTVL(ctx context.Context, chainID int64, vault string) (types.Raw, uint8, string, error) {
	var (
		tvl      types.Raw
		decimals int16
		asset    string
	)
	if err := s.pool.QueryRow(ctx, qVaultTVL, chainID, vault).Scan(&tvl, &decimals, &asset); err != nil {
		return tvl, 0, "", fmt.Errorf("vault tvl %d/%s: %w", chainID, vault, err)
	}
	return tvl, uint8(decimals), asset, nil
}
