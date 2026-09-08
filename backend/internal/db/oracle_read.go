package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Every read joins the scale in rather than leaving the caller to find it. A raw value and its
// decimals travel together from the query to the JSON response.

const qListFeeds = `
	SELECT chain_id, feed_id, name, decimals, active
	FROM oracle_feeds
	WHERE chain_id = $1
	ORDER BY name
	LIMIT $2 OFFSET $3
`

const qGetFeed = `
	SELECT chain_id, feed_id, name, decimals, active
	FROM oracle_feeds
	WHERE chain_id = $1 AND feed_id = $2
`

const qRoundColumns = `
	r.chain_id, r.round_id, r.feed_id, f.name, r.state, r.opened_at, r.deadline, r.settled_at,
	r.eligible_count, r.node_set_version, r.submission_count,
	COALESCE(r.aggregated_value, 0), f.decimals, r.tx_hash, r.block_number
`

const qListRoundsByFeed = `
	SELECT ` + qRoundColumns + `
	FROM oracle_rounds r
	JOIN oracle_feeds f ON f.chain_id = r.chain_id AND f.feed_id = r.feed_id
	WHERE r.chain_id = $1 AND r.feed_id = $2
	ORDER BY r.round_id DESC
	LIMIT $3 OFFSET $4
`

const qGetRound = `
	SELECT ` + qRoundColumns + `
	FROM oracle_rounds r
	JOIN oracle_feeds f ON f.chain_id = r.chain_id AND f.feed_id = r.feed_id
	WHERE r.chain_id = $1 AND r.round_id = $2
`

const qListSubmissions = `
	SELECT s.chain_id, s.round_id, s.node_address, s.value, f.decimals, s.is_outlier,
	       s.tx_hash, s.log_index, s.block_number, s.submitted_at
	FROM oracle_submissions s
	JOIN oracle_rounds r ON r.chain_id = s.chain_id AND r.round_id = s.round_id
	JOIN oracle_feeds f ON f.chain_id = r.chain_id AND f.feed_id = r.feed_id
	WHERE s.chain_id = $1 AND s.round_id = $2
	ORDER BY s.log_index
	LIMIT $3 OFFSET $4
`

const qNodeColumns = `
	n.chain_id, n.address, n.stake_asset, n.staked_amount, n.slashed_total, n.pending_unstake,
	a.decimals, n.claimable_at, n.missed_rounds, n.active, n.registered_at
`

const qListNodes = `
	SELECT ` + qNodeColumns + `
	FROM oracle_nodes n
	JOIN assets a ON a.chain_id = n.chain_id AND a.address = n.stake_asset
	WHERE n.chain_id = $1
	ORDER BY n.staked_amount DESC
	LIMIT $2 OFFSET $3
`

const qGetNode = `
	SELECT ` + qNodeColumns + `
	FROM oracle_nodes n
	JOIN assets a ON a.chain_id = n.chain_id AND a.address = n.stake_asset
	WHERE n.chain_id = $1 AND n.address = $2
`

// ErrNotFound distinguishes an absent row from a failed query, so a handler can map it to 404
// rather than 500.
var ErrNotFound = errors.New("not found")

func (s *Store) ListOracleFeeds(ctx context.Context, chainID int64, limit, offset int) ([]types.OracleFeed, error) {
	rows, err := s.pool.Query(ctx, qListFeeds, chainID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list feeds: %w", err)
	}
	defer rows.Close()

	out := make([]types.OracleFeed, 0, limit)
	for rows.Next() {
		feed, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, feed)
	}
	return out, rows.Err()
}

func (s *Store) OracleFeed(ctx context.Context, chainID int64, feedID string) (types.OracleFeed, error) {
	feed, err := scanFeed(s.pool.QueryRow(ctx, qGetFeed, chainID, feedID))
	if errors.Is(err, pgx.ErrNoRows) {
		return feed, ErrNotFound
	}
	return feed, err
}

func (s *Store) ListOracleRounds(ctx context.Context, chainID int64, feedID string, limit, offset int) ([]types.OracleRound, error) {
	rows, err := s.pool.Query(ctx, qListRoundsByFeed, chainID, feedID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list rounds: %w", err)
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

func (s *Store) OracleRound(ctx context.Context, chainID int64, roundID types.Raw) (types.OracleRound, error) {
	round, err := scanRound(s.pool.QueryRow(ctx, qGetRound, chainID, roundID))
	if errors.Is(err, pgx.ErrNoRows) {
		return round, ErrNotFound
	}
	return round, err
}

func (s *Store) ListOracleSubmissions(ctx context.Context, chainID int64, roundID types.Raw, limit, offset int) ([]types.OracleSubmission, error) {
	rows, err := s.pool.Query(ctx, qListSubmissions, chainID, roundID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list submissions: %w", err)
	}
	defer rows.Close()

	out := make([]types.OracleSubmission, 0, limit)
	for rows.Next() {
		var (
			sub      types.OracleSubmission
			decimals int16
		)
		if err := rows.Scan(&sub.ChainID, &sub.RoundID, &sub.Node, &sub.Value, &decimals,
			&sub.IsOutlier, &sub.TxHash, &sub.LogIndex, &sub.BlockNumber, &sub.SubmittedAt); err != nil {
			return nil, fmt.Errorf("scan submission: %w", err)
		}
		sub.Decimals = uint8(decimals)
		out = append(out, sub)
	}
	return out, rows.Err()
}

func (s *Store) ListOracleNodes(ctx context.Context, chainID int64, limit, offset int) ([]types.OracleNode, error) {
	rows, err := s.pool.Query(ctx, qListNodes, chainID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()

	out := make([]types.OracleNode, 0, limit)
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func (s *Store) OracleNode(ctx context.Context, chainID int64, address string) (types.OracleNode, error) {
	node, err := scanNode(s.pool.QueryRow(ctx, qGetNode, chainID, address))
	if errors.Is(err, pgx.ErrNoRows) {
		return node, ErrNotFound
	}
	return node, err
}

// scanner covers both pgx.Row and pgx.Rows so single and list reads share one decoder.
type scanner interface {
	Scan(dest ...any) error
}

func scanFeed(row scanner) (types.OracleFeed, error) {
	var (
		feed     types.OracleFeed
		decimals int16
	)
	if err := row.Scan(&feed.ChainID, &feed.FeedID, &feed.Name, &decimals, &feed.Active); err != nil {
		return feed, err
	}
	feed.Decimals = uint8(decimals)
	return feed, nil
}

func scanRound(row scanner) (types.OracleRound, error) {
	var (
		round    types.OracleRound
		decimals int16
	)
	if err := row.Scan(&round.ChainID, &round.RoundID, &round.FeedID, &round.FeedName, &round.State,
		&round.OpenedAt, &round.Deadline, &round.SettledAt, &round.EligibleCount,
		&round.NodeSetVersion, &round.SubmissionCount, &round.AggregatedValue, &decimals,
		&round.TxHash, &round.BlockNumber); err != nil {
		return round, err
	}
	round.Decimals = uint8(decimals)
	return round, nil
}

func scanNode(row scanner) (types.OracleNode, error) {
	var (
		node     types.OracleNode
		decimals int16
	)
	if err := row.Scan(&node.ChainID, &node.Address, &node.StakeAsset, &node.StakedAmount,
		&node.SlashedTotal, &node.PendingUnstake, &decimals, &node.ClaimableAt,
		&node.MissedRounds, &node.Active, &node.RegisteredAt); err != nil {
		return node, err
	}
	node.Decimals = uint8(decimals)
	return node, nil
}
