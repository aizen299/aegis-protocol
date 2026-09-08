package db

import (
	"context"
	"fmt"
	"time"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// Feed names arrive only in FeedRegistered. A round can still be indexed for a feed that has not
// been seen — an indexer started after deployment, or a backfill from a later block — so the row is
// created with the identifier as a placeholder name and corrected when the registration event is
// seen. That is deliberately different from how asset decimals are handled, where a missing value
// fails the batch: a wrong display name is cosmetic, a wrong scale silently misstates every amount.
const qUpsertFeed = `
	INSERT INTO oracle_feeds (chain_id, feed_id, name, decimals, active)
	VALUES ($1, $2, $3, $4, TRUE)
	ON CONFLICT (chain_id, feed_id)
	DO UPDATE SET name = EXCLUDED.name, active = TRUE
`

const qEnsureFeedPlaceholder = `
	INSERT INTO oracle_feeds (chain_id, feed_id, name, decimals)
	VALUES ($1, $2, $2, $3)
	ON CONFLICT (chain_id, feed_id) DO NOTHING
`

const qDeactivateFeed = `
	UPDATE oracle_feeds SET active = FALSE WHERE chain_id = $1 AND feed_id = $2
`

const qInsertRound = `
	INSERT INTO oracle_rounds
		(chain_id, round_id, feed_id, state, opened_at, deadline, eligible_count, node_set_version,
		 tx_hash, log_index, block_number)
	VALUES ($1, $2, $3, 'open', $4, $5, $6, $7, $8, $9, $10)
	ON CONFLICT (chain_id, round_id) DO NOTHING
`

// Guarded on the current state so a replayed batch cannot walk a settled round backwards.
const qMarkQuorumMet = `
	UPDATE oracle_rounds
	SET state = 'quorum_met', submission_count = $3
	WHERE chain_id = $1 AND round_id = $2 AND state = 'open'
`

const qSettleRound = `
	UPDATE oracle_rounds
	SET state = 'settled', aggregated_value = $3, submission_count = $4, settled_at = $5
	WHERE chain_id = $1 AND round_id = $2 AND state IN ('open', 'quorum_met')
`

const qFailRound = `
	UPDATE oracle_rounds
	SET state = 'failed', submission_count = $3, settled_at = $4
	WHERE chain_id = $1 AND round_id = $2 AND state IN ('open', 'quorum_met')
`

const qInsertSubmission = `
	INSERT INTO oracle_submissions
		(chain_id, round_id, node_address, value, signature, tx_hash, log_index, block_number, submitted_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	ON CONFLICT (chain_id, tx_hash, log_index) DO NOTHING
`

const qUpsertNode = `
	INSERT INTO oracle_nodes (chain_id, address, stake_asset, staked_amount, registered_at, active)
	VALUES ($1, $2, $3, $4, $5, TRUE)
	ON CONFLICT (chain_id, address)
	DO UPDATE SET staked_amount = EXCLUDED.staked_amount, active = TRUE
`

const qSetNodeStake = `
	UPDATE oracle_nodes SET staked_amount = $3 WHERE chain_id = $1 AND address = $2
`

const qSetNodeUnstake = `
	UPDATE oracle_nodes
	SET pending_unstake = $3, claimable_at = $4, active = $5
	WHERE chain_id = $1 AND address = $2
`

const qSetNodeActive = `
	UPDATE oracle_nodes SET active = $3 WHERE chain_id = $1 AND address = $2
`

const qRecordSlash = `
	INSERT INTO oracle_slashings
		(chain_id, node_address, amount, reason, decided_at, executed_at, tx_hash, log_index, block_number)
	VALUES ($1, $2, $3, $4, $5, $5, $6, $7, $8)
	ON CONFLICT (chain_id, tx_hash, log_index) DO NOTHING
`

const qApplySlash = `
	UPDATE oracle_nodes
	SET staked_amount = $3, slashed_total = slashed_total + $4
	WHERE chain_id = $1 AND address = $2
`

// Feed records a feed's registration.
type Feed struct {
	ChainID  int64
	FeedID   string
	Name     string
	Decimals uint8
}

// Round mirrors a RoundStarted event.
type Round struct {
	ChainID        int64
	RoundID        types.Raw
	FeedID         string
	OpenedAt       time.Time
	Deadline       time.Time
	EligibleCount  int32
	NodeSetVersion types.Raw
	TxHash         string
	LogIndex       uint
	BlockNumber    uint64
}

// Submission mirrors a SubmissionReceived event.
type Submission struct {
	ChainID     int64
	RoundID     types.Raw
	Node        string
	Value       types.Raw
	Signature   []byte
	TxHash      string
	LogIndex    uint
	BlockNumber uint64
	SubmittedAt time.Time
}

// NodeStakeEvent carries whichever staking fields the originating event changed.
type NodeStakeEvent struct {
	ChainID      int64
	Address      string
	StakeAsset   string
	StakedAmount types.Raw
	At           time.Time
}

// Slash mirrors a NodeSlashed event.
type Slash struct {
	ChainID        int64
	Node           string
	Amount         types.Raw
	RemainingStake types.Raw
	Reason         string
	TxHash         string
	LogIndex       uint
	BlockNumber    uint64
	At             time.Time
}

func (s *Store) UpsertOracleFeed(ctx context.Context, f Feed) error {
	if _, err := s.pool.Exec(ctx, qUpsertFeed, f.ChainID, f.FeedID, f.Name, int16(f.Decimals)); err != nil {
		return fmt.Errorf("upsert feed %s: %w", f.FeedID, err)
	}
	return nil
}

func (s *Store) EnsureOracleFeed(ctx context.Context, chainID int64, feedID string, decimals uint8) error {
	if _, err := s.pool.Exec(ctx, qEnsureFeedPlaceholder, chainID, feedID, int16(decimals)); err != nil {
		return fmt.Errorf("ensure feed %s: %w", feedID, err)
	}
	return nil
}

func (s *Store) DeactivateOracleFeed(ctx context.Context, chainID int64, feedID string) error {
	if _, err := s.pool.Exec(ctx, qDeactivateFeed, chainID, feedID); err != nil {
		return fmt.Errorf("deactivate feed %s: %w", feedID, err)
	}
	return nil
}

func (s *Store) InsertOracleRound(ctx context.Context, r Round) error {
	_, err := s.pool.Exec(ctx, qInsertRound, r.ChainID, r.RoundID, r.FeedID, r.OpenedAt, r.Deadline,
		r.EligibleCount, r.NodeSetVersion, r.TxHash, int32(r.LogIndex), int64(r.BlockNumber))
	if err != nil {
		return fmt.Errorf("insert round %s: %w", r.RoundID, err)
	}
	return nil
}

func (s *Store) MarkOracleRoundQuorumMet(ctx context.Context, chainID int64, roundID types.Raw, count int32) error {
	if _, err := s.pool.Exec(ctx, qMarkQuorumMet, chainID, roundID, count); err != nil {
		return fmt.Errorf("mark quorum %s: %w", roundID, err)
	}
	return nil
}

func (s *Store) SettleOracleRound(ctx context.Context, chainID int64, roundID, value types.Raw, count int32, at time.Time) error {
	if _, err := s.pool.Exec(ctx, qSettleRound, chainID, roundID, value, count, at); err != nil {
		return fmt.Errorf("settle round %s: %w", roundID, err)
	}
	return nil
}

func (s *Store) FailOracleRound(ctx context.Context, chainID int64, roundID types.Raw, count int32, at time.Time) error {
	if _, err := s.pool.Exec(ctx, qFailRound, chainID, roundID, count, at); err != nil {
		return fmt.Errorf("fail round %s: %w", roundID, err)
	}
	return nil
}

func (s *Store) InsertOracleSubmission(ctx context.Context, sub Submission) error {
	_, err := s.pool.Exec(ctx, qInsertSubmission, sub.ChainID, sub.RoundID, sub.Node, sub.Value,
		sub.Signature, sub.TxHash, int32(sub.LogIndex), int64(sub.BlockNumber), sub.SubmittedAt)
	if err != nil {
		return fmt.Errorf("insert submission %s#%d: %w", sub.TxHash, sub.LogIndex, err)
	}
	return nil
}

func (s *Store) UpsertOracleNode(ctx context.Context, e NodeStakeEvent) error {
	_, err := s.pool.Exec(ctx, qUpsertNode, e.ChainID, e.Address, e.StakeAsset, e.StakedAmount, e.At)
	if err != nil {
		return fmt.Errorf("upsert node %s: %w", e.Address, err)
	}
	return nil
}

func (s *Store) SetOracleNodeStake(ctx context.Context, chainID int64, node string, stake types.Raw) error {
	if _, err := s.pool.Exec(ctx, qSetNodeStake, chainID, node, stake); err != nil {
		return fmt.Errorf("set stake %s: %w", node, err)
	}
	return nil
}

func (s *Store) SetOracleNodeUnstake(ctx context.Context, chainID int64, node string, pending types.Raw, claimableAt *time.Time, active bool) error {
	if _, err := s.pool.Exec(ctx, qSetNodeUnstake, chainID, node, pending, claimableAt, active); err != nil {
		return fmt.Errorf("set unstake %s: %w", node, err)
	}
	return nil
}

func (s *Store) SetOracleNodeActive(ctx context.Context, chainID int64, node string, active bool) error {
	if _, err := s.pool.Exec(ctx, qSetNodeActive, chainID, node, active); err != nil {
		return fmt.Errorf("set active %s: %w", node, err)
	}
	return nil
}

// RecordOracleSlash writes the audit row and applies the balance change. Both in one transaction:
// a slash recorded without being applied, or applied without being recorded, is worse than either.
func (s *Store) RecordOracleSlash(ctx context.Context, sl Slash) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin slash tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, qRecordSlash, sl.ChainID, sl.Node, sl.Amount, sl.Reason, sl.At,
		sl.TxHash, int32(sl.LogIndex), int64(sl.BlockNumber)); err != nil {
		return fmt.Errorf("record slash %s: %w", sl.Node, err)
	}
	if _, err := tx.Exec(ctx, qApplySlash, sl.ChainID, sl.Node, sl.RemainingStake, sl.Amount); err != nil {
		return fmt.Errorf("apply slash %s: %w", sl.Node, err)
	}

	return tx.Commit(ctx)
}
