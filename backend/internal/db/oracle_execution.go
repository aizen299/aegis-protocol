package db

import (
	"context"
	"fmt"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/oracle"
)

// Claiming and returning in one statement. Two executors running concurrently would otherwise both
// read the same rows and both submit; the UPDATE ... RETURNING makes the claim atomic, and
// FOR UPDATE SKIP LOCKED lets a second executor take different work rather than block.
const qClaimPendingSlashes = `
	UPDATE oracle_slashings
	SET attempts = attempts + 1
	WHERE id IN (
		SELECT id FROM oracle_slashings
		WHERE chain_id = $1
		  AND executed_at IS NULL
		  AND submitted_at IS NULL
		  AND abandoned_at IS NULL
		  AND round_id IS NOT NULL
		  AND attempts < $3
		ORDER BY decided_at
		LIMIT $2
		FOR UPDATE SKIP LOCKED
	)
	RETURNING id::text, chain_id, node_address, round_id, amount, reason, attempts
`

const qMarkSlashSubmitted = `
	UPDATE oracle_slashings
	SET submitted_at = $3, tx_hash = $2
	WHERE id = $1::uuid AND submitted_at IS NULL
`

const qMarkSlashAbandoned = `
	UPDATE oracle_slashings
	SET abandoned_at = $3, abandoned_reason = $2
	WHERE id = $1::uuid AND abandoned_at IS NULL
`

func (s *Store) ClaimPendingSlashes(ctx context.Context, chainID int64, limit, maxAttempts int) ([]oracle.PendingSlash, error) {
	rows, err := s.pool.Query(ctx, qClaimPendingSlashes, chainID, limit, maxAttempts)
	if err != nil {
		return nil, fmt.Errorf("claim pending slashes: %w", err)
	}
	defer rows.Close()

	out := make([]oracle.PendingSlash, 0, limit)
	for rows.Next() {
		var p oracle.PendingSlash
		if err := rows.Scan(&p.ID, &p.ChainID, &p.Node, &p.RoundID, &p.Amount, &p.Reason, &p.Attempts); err != nil {
			return nil, fmt.Errorf("scan pending slash: %w", err)
		}
		// Attempts is post-increment; the executor reasons about attempts already made.
		p.Attempts--
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) MarkSlashSubmitted(ctx context.Context, id, txHash string, at time.Time) error {
	if _, err := s.pool.Exec(ctx, qMarkSlashSubmitted, id, txHash, at); err != nil {
		return fmt.Errorf("mark slash submitted %s: %w", id, err)
	}
	return nil
}

func (s *Store) MarkSlashAbandoned(ctx context.Context, id, reason string, at time.Time) error {
	if _, err := s.pool.Exec(ctx, qMarkSlashAbandoned, id, reason, at); err != nil {
		return fmt.Errorf("mark slash abandoned %s: %w", id, err)
	}
	return nil
}
