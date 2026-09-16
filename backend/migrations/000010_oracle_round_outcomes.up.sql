-- What each node's silence or submission counted as, per round. docs/v1.3-missed-round-slashing-plan.md.
--
-- A streak depends on a node's outcomes in round order, so each outcome is recorded once and the penalty
-- recomputed from the whole sequence. Judging the same round twice records the same row.
CREATE TABLE oracle_round_outcomes (
    chain_id  BIGINT NOT NULL,
    round_id  NUMERIC(78, 0) NOT NULL,
    node      TEXT NOT NULL,
    outcome   TEXT NOT NULL CHECK (outcome IN ('submitted', 'missed', 'excused')),
    -- Why an excused silence was excused. Empty for the other two.
    why       TEXT NOT NULL DEFAULT '',
    judged_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (chain_id, round_id, node)
);

CREATE INDEX oracle_round_outcomes_by_node ON oracle_round_outcomes (chain_id, node, round_id);

-- Misses are judged in strict round order, separately from aggregation, which runs in settlement order.
-- A streak judged out of order would be missing a round, and a slash already decided cannot be taken
-- back when the earlier round is judged later.
ALTER TABLE oracle_rounds ADD COLUMN misses_judged_at TIMESTAMPTZ;

CREATE INDEX oracle_rounds_unjudged ON oracle_rounds (chain_id, round_id) WHERE misses_judged_at IS NULL;

-- Nothing ever wrote this column, and the Nodes page displayed its permanent zero under a label saying
-- misses were recorded. The count is now read from the outcomes above, so there is one source and no
-- copy to drift from it.
ALTER TABLE oracle_nodes DROP COLUMN missed_rounds;
