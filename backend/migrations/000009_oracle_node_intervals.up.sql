-- The periods a node spent in the active set, as node-set versions. docs/v1.3-missed-round-slashing-plan.md.
--
-- A round freezes its eligible set as a version when it opens, so a node was expected to submit to a
-- round exactly when the round's version falls in [activated_version, deactivated_version). This is
-- the only record that can answer that after the fact: the contract's isEligibleAt reads current
-- state, so once a node leaves it reports every past version as ineligible.
--
-- Kept apart from oracle_nodes, deliberately. A registration emits the activation before the
-- registration itself, so the node's row does not exist yet when the first interval opens.
CREATE TABLE oracle_node_intervals (
    chain_id             BIGINT NOT NULL,
    node                 TEXT NOT NULL,
    activated_version    NUMERIC(78, 0) NOT NULL,
    activated_at         TIMESTAMPTZ NOT NULL,
    -- NULL while the node is still active.
    deactivated_version  NUMERIC(78, 0),
    deactivated_at       TIMESTAMPTZ,
    -- UNSTAKE_REQUESTED, MANUAL, BELOW_STAKE_FLOOR. Decides whether a silence it caused is slashable.
    deactivation_reason  TEXT,
    PRIMARY KEY (chain_id, node, activated_version),
    CHECK (deactivated_version IS NULL OR deactivated_version > activated_version),
    CHECK ((deactivated_version IS NULL) = (deactivated_at IS NULL)),
    CHECK ((deactivated_version IS NULL) = (deactivation_reason IS NULL))
);

-- Eligibility queries ask "which intervals contain version v" for every node on a chain.
CREATE INDEX oracle_node_intervals_by_version
    ON oracle_node_intervals (chain_id, activated_version, deactivated_version);
