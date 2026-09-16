package db

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aizen299/aegis-protocol/backend/internal/oracle"
	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// The lowest round whose misses are unjudged, whatever its state. Judgement stops at it if it is not
// finished: a later round judged first would compute its streaks without this one.
const qNextRoundToJudge = `
	SELECT ` + qRoundColumns + `
	FROM oracle_rounds r
	JOIN oracle_feeds f ON f.chain_id = r.chain_id AND f.feed_id = r.feed_id
	WHERE r.chain_id = $1 AND r.misses_judged_at IS NULL
	ORDER BY r.round_id ASC
	LIMIT 1
`

// Every node that submitted, with no limit. The listing endpoint pages at a configured maximum; a
// submitter set cut off there would turn submissions past the cap into misses.
const qRoundSubmitters = `
	SELECT DISTINCT node_address FROM oracle_submissions
	WHERE chain_id = $1 AND round_id = $2
`

const qIntervalsAt = `
	SELECT node, activated_version, deactivated_version, deactivated_at, COALESCE(deactivation_reason, '')
	FROM oracle_node_intervals
	WHERE chain_id = $1 AND activated_version <= $2
	  AND (deactivated_version IS NULL OR deactivated_version > $2)
`

const qRecordOutcome = `
	INSERT INTO oracle_round_outcomes (chain_id, round_id, node, outcome, why)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT (chain_id, round_id, node) DO NOTHING
`

const qOutcomeHistory = `
	SELECT outcome FROM oracle_round_outcomes
	WHERE chain_id = $1 AND node = $2
	ORDER BY round_id ASC
`

const qMarkMissesJudged = `
	UPDATE oracle_rounds SET misses_judged_at = NOW()
	WHERE chain_id = $1 AND round_id = $2 AND misses_judged_at IS NULL
`

var outcomeNames = map[oracle.Outcome]string{
	oracle.Submitted: "submitted",
	oracle.Missed:    "missed",
	oracle.Excused:   "excused",
}

func (s *Store) NextRoundToJudge(ctx context.Context, chainID int64) (types.OracleRound, bool, error) {
	round, err := scanRound(s.pool.QueryRow(ctx, qNextRoundToJudge, chainID))
	if errors.Is(err, pgx.ErrNoRows) {
		return types.OracleRound{}, false, nil
	}
	if err != nil {
		return types.OracleRound{}, false, fmt.Errorf("next round to judge: %w", err)
	}
	return round, true, nil
}

func (s *Store) RoundSubmitters(ctx context.Context, chainID int64, roundID types.Raw) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, qRoundSubmitters, chainID, roundID)
	if err != nil {
		return nil, fmt.Errorf("round submitters: %w", err)
	}
	defer rows.Close()

	out := make(map[string]bool)
	for rows.Next() {
		var node string
		if err := rows.Scan(&node); err != nil {
			return nil, err
		}
		out[node] = true
	}
	return out, rows.Err()
}

func (s *Store) NodeIntervalsAt(ctx context.Context, chainID int64, version types.Raw) ([]oracle.NodeInterval, error) {
	rows, err := s.pool.Query(ctx, qIntervalsAt, chainID, version)
	if err != nil {
		return nil, fmt.Errorf("intervals at version: %w", err)
	}
	defer rows.Close()

	var out []oracle.NodeInterval
	for rows.Next() {
		var (
			iv            oracle.NodeInterval
			activated     types.Raw
			deactivated   *types.Raw
			deactivatedAt *time.Time
		)
		if err := rows.Scan(&iv.Node, &activated, &deactivated, &deactivatedAt, &iv.Reason); err != nil {
			return nil, err
		}
		iv.Activated = activated.Big()
		if deactivated != nil {
			iv.Deactivated = new(big.Int).Set(deactivated.Big())
		}
		iv.DeactivatedAt = deactivatedAt
		out = append(out, iv)
	}
	return out, rows.Err()
}

func (s *Store) RecordRoundOutcomes(ctx context.Context, chainID int64, roundID types.Raw, verdicts []oracle.Verdict) error {
	for _, v := range verdicts {
		if _, err := s.pool.Exec(ctx, qRecordOutcome, chainID, roundID, v.Node, outcomeNames[v.Outcome], v.Why); err != nil {
			return fmt.Errorf("record outcome %s: %w", v.Node, err)
		}
	}
	return nil
}

func (s *Store) NodeOutcomeHistory(ctx context.Context, chainID int64, node string) ([]oracle.Outcome, error) {
	rows, err := s.pool.Query(ctx, qOutcomeHistory, chainID, node)
	if err != nil {
		return nil, fmt.Errorf("outcome history: %w", err)
	}
	defer rows.Close()

	byName := map[string]oracle.Outcome{"submitted": oracle.Submitted, "missed": oracle.Missed, "excused": oracle.Excused}
	var out []oracle.Outcome
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		outcome, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown outcome %q", name)
		}
		out = append(out, outcome)
	}
	return out, rows.Err()
}

func (s *Store) MarkMissesJudged(ctx context.Context, chainID int64, roundID types.Raw) error {
	if _, err := s.pool.Exec(ctx, qMarkMissesJudged, chainID, roundID); err != nil {
		return fmt.Errorf("mark misses judged: %w", err)
	}
	return nil
}
