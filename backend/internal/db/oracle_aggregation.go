package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aizen299/aegis-protocol/backend/internal/oracle"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

const qUnaggregatedRounds = `
	SELECT ` + qRoundColumns + `
	FROM oracle_rounds r
	JOIN oracle_feeds f ON f.chain_id = r.chain_id AND f.feed_id = r.feed_id
	WHERE r.chain_id = $1 AND r.aggregated_at IS NULL AND r.state IN ('settled', 'failed')
	ORDER BY r.settled_at
	LIMIT $2
`

const qSubmissionSignature = `
	SELECT signature
	FROM oracle_submissions
	WHERE chain_id = $1 AND round_id = $2 AND node_address = $3
`

const qRecordVerification = `
	UPDATE oracle_submissions
	SET signature_valid = $4, is_outlier = $5
	WHERE chain_id = $1 AND round_id = $2 AND node_address = $3
`

// One decision per node per round per reason. A replayed analysis records nothing new, which is
// what lets the round be re-analysed safely after a crash.
const qRecordSlashDecision = `
	INSERT INTO oracle_slashings (chain_id, node_address, round_id, amount, reason, decided_at)
	SELECT $1, $2, $3, $4, $5, NOW()
	WHERE NOT EXISTS (
		SELECT 1 FROM oracle_slashings
		WHERE chain_id = $1 AND node_address = $2 AND round_id = $3 AND reason = $5
	)
`

const qMarkRoundAggregated = `
	UPDATE oracle_rounds
	SET aggregated_at = $5, service_value = $3, value_mismatch = $4
	WHERE chain_id = $1 AND round_id = $2 AND aggregated_at IS NULL
`

func (s *Store) UnaggregatedRounds(ctx context.Context, chainID int64, limit int) ([]types.OracleRound, error) {
	rows, err := s.pool.Query(ctx, qUnaggregatedRounds, chainID, limit)
	if err != nil {
		return nil, fmt.Errorf("unaggregated rounds: %w", err)
	}
	defer rows.Close()

	out := make([]types.OracleRound, 0, limit)
	for rows.Next() {
		round, err := scanRound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, round)
	}
	return out, rows.Err()
}

func (s *Store) SubmissionSignature(ctx context.Context, chainID int64, roundID types.Raw, node string) ([]byte, error) {
	var signature []byte
	err := s.pool.QueryRow(ctx, qSubmissionSignature, chainID, roundID, node).Scan(&signature)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("submission signature %s/%s: %w", roundID, node, err)
	}
	return signature, nil
}

func (s *Store) RecordSubmissionVerification(ctx context.Context, chainID int64, roundID types.Raw, node string, valid, outlier bool) error {
	_, err := s.pool.Exec(ctx, qRecordVerification, chainID, roundID, node, valid, outlier)
	if err != nil {
		return fmt.Errorf("record verification %s/%s: %w", roundID, node, err)
	}
	return nil
}

// RecordSlashDecision writes the decision only. Execution is a separate step against the chain, so
// the reason a node's stake moved is durable before anything is sent.
func (s *Store) RecordSlashDecision(ctx context.Context, d oracle.SlashDecision) error {
	_, err := s.pool.Exec(ctx, qRecordSlashDecision, d.ChainID, d.Node, d.RoundID, d.Amount, d.Reason)
	if err != nil {
		return fmt.Errorf("record slash decision %s/%s: %w", d.Node, d.Reason, err)
	}
	return nil
}

func (s *Store) MarkRoundAggregated(ctx context.Context, chainID int64, roundID, serviceValue types.Raw, mismatch bool, at time.Time) error {
	_, err := s.pool.Exec(ctx, qMarkRoundAggregated, chainID, roundID, serviceValue, mismatch, at)
	if err != nil {
		return fmt.Errorf("mark round aggregated %s: %w", roundID, err)
	}
	return nil
}
