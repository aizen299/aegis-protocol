-- Governance actions received and executed on another chain. docs/v2.0-solana-plan.md §18.8.
--
-- A row is keyed where the action runs: the receiving chain, and the Wormhole message that carried it.
-- It names the proposal's chain and Timelock operation it came from, which is how a dispatched proposal
-- finds out what became of it. No foreign key reaches the proposal: the receiving chain may be indexed
-- by another indexer, before or after the proposal's.
CREATE TABLE governance_remote_actions (
    chain_id        BIGINT NOT NULL,
    receiver        TEXT NOT NULL,
    emitter_chain   INTEGER NOT NULL CHECK (emitter_chain BETWEEN 0 AND 65535),
    sequence        NUMERIC(20, 0) NOT NULL CHECK (sequence >= 0),
    source_chain_id BIGINT NOT NULL,
    operation_id    NUMERIC(20, 0) NOT NULL CHECK (operation_id > 0),
    target          TEXT NOT NULL,
    -- As the proposal declared it. Not what was spent: the receiver caps measured outflow instead.
    declared_value  NUMERIC(78, 0) NOT NULL CHECK (declared_value >= 0),
    accounts_hash   BYTEA NOT NULL CHECK (octet_length(accounts_hash) = 32),
    status          TEXT NOT NULL CHECK (status IN ('pending', 'executed', 'cancelled')),
    executable_at   TIMESTAMPTZ NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL,
    received_tx     TEXT NOT NULL,
    closed_at       TIMESTAMPTZ,
    closed_tx       TEXT,
    -- The executor, or whoever cancelled.
    closed_by       TEXT,
    PRIMARY KEY (chain_id, emitter_chain, sequence),
    CHECK ((status = 'pending') = (closed_at IS NULL))
);

CREATE INDEX governance_remote_actions_by_operation
    ON governance_remote_actions (source_chain_id, operation_id, chain_id);

CREATE INDEX governance_remote_actions_by_status
    ON governance_remote_actions (chain_id, status, executable_at);
