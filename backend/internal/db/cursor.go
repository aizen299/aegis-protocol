package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const qLoadCursor = `
	SELECT last_block
	FROM indexer_cursors
	WHERE service_name = $1 AND chain_id = $2
`

const qSaveCursor = `
	INSERT INTO indexer_cursors (service_name, chain_id, last_block)
	VALUES ($1, $2, $3)
	ON CONFLICT (service_name, chain_id)
	DO UPDATE SET last_block = EXCLUDED.last_block
	WHERE indexer_cursors.last_block < EXCLUDED.last_block
`

// LoadCursor returns the last processed block for a service on a chain, and whether a cursor
// existed. Cursors are per-chain: one indexer process serves one chain.
func (s *Store) LoadCursor(ctx context.Context, service string, chainID int64) (uint64, bool, error) {
	var last int64
	err := s.pool.QueryRow(ctx, qLoadCursor, service, chainID).Scan(&last)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("load cursor %s/%d: %w", service, chainID, err)
	}
	return uint64(last), true, nil
}

// SaveCursor advances the cursor. The guard makes it monotonic, so a stale write from a restarted
// process cannot rewind a cursor another process has already advanced.
func (s *Store) SaveCursor(ctx context.Context, service string, chainID int64, block uint64) error {
	if _, err := s.pool.Exec(ctx, qSaveCursor, service, chainID, int64(block)); err != nil {
		return fmt.Errorf("save cursor %s/%d: %w", service, chainID, err)
	}
	return nil
}
