-- v0.2 schema: oracle feeds, node registry, rounds, submissions, and slashing decisions.
--
-- Amounts here follow the same rule as v0.1: raw base units, unscaled, with the scale recorded
-- alongside. That includes the oracle's own aggregated value. NUMERIC(38,18) cannot hold every
-- uint256 a node may legitimately submit, and an indexer that cannot store a valid on-chain value
-- stalls — so the narrower column is a correctness problem, not a style choice.

CREATE TABLE oracle_feeds (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    feed_id       TEXT NOT NULL,
    name          TEXT NOT NULL,
    -- Fixed by OracleRounds rather than by any token, but still recorded: a value without its
    -- scale is uninterpretable regardless of where the scale comes from.
    decimals      SMALLINT NOT NULL DEFAULT 18 CHECK (decimals >= 0 AND decimals <= 38),
    active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, feed_id)
);

CREATE TRIGGER oracle_feeds_set_updated_at
    BEFORE UPDATE ON oracle_feeds
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Stake is a token amount, so it carries a foreign key to the asset that gives it a scale, exactly
-- as vault amounts do.
CREATE TABLE oracle_nodes (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id       BIGINT NOT NULL,
    address        TEXT NOT NULL,
    stake_asset    TEXT NOT NULL,
    staked_amount  NUMERIC(78, 0) NOT NULL DEFAULT 0 CHECK (staked_amount >= 0),
    slashed_total  NUMERIC(78, 0) NOT NULL DEFAULT 0 CHECK (slashed_total >= 0),
    pending_unstake NUMERIC(78, 0) NOT NULL DEFAULT 0 CHECK (pending_unstake >= 0),
    claimable_at   TIMESTAMPTZ,
    missed_rounds  INT NOT NULL DEFAULT 0,
    active         BOOLEAN NOT NULL DEFAULT TRUE,
    registered_at  TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, address),
    FOREIGN KEY (chain_id, stake_asset) REFERENCES assets(chain_id, address)
);

CREATE TRIGGER oracle_nodes_set_updated_at
    BEFORE UPDATE ON oracle_nodes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- eligible_count and node_set_version are the snapshot the contract froze at open. Storing them
-- means quorum can be audited off-chain against the set that actually applied, rather than against
-- whatever the node set looks like now.
CREATE TABLE oracle_rounds (
    id               BIGSERIAL PRIMARY KEY,
    chain_id         BIGINT NOT NULL,
    round_id         NUMERIC(78, 0) NOT NULL,
    feed_id          TEXT NOT NULL,
    state            TEXT NOT NULL DEFAULT 'open'
                     CHECK (state IN ('open', 'quorum_met', 'settled', 'failed')),
    opened_at        TIMESTAMPTZ NOT NULL,
    deadline         TIMESTAMPTZ NOT NULL,
    settled_at       TIMESTAMPTZ,
    eligible_count   INT NOT NULL,
    node_set_version NUMERIC(78, 0) NOT NULL,
    submission_count INT NOT NULL DEFAULT 0,
    aggregated_value NUMERIC(78, 0) CHECK (aggregated_value IS NULL OR aggregated_value >= 0),
    tx_hash          TEXT NOT NULL,
    log_index        INT NOT NULL,
    block_number     BIGINT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, round_id),
    UNIQUE (chain_id, tx_hash, log_index),
    FOREIGN KEY (chain_id, feed_id) REFERENCES oracle_feeds(chain_id, feed_id)
);

CREATE TRIGGER oracle_rounds_set_updated_at
    BEFORE UPDATE ON oracle_rounds
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Two uniqueness constraints by design: the semantic key states the protocol rule (one submission
-- per node per round), the event key makes ingestion idempotent. See docs/database.md
-- "Event Identity".
CREATE TABLE oracle_submissions (
    id            BIGSERIAL PRIMARY KEY,
    chain_id      BIGINT NOT NULL,
    round_id      NUMERIC(78, 0) NOT NULL,
    node_address  TEXT NOT NULL,
    value         NUMERIC(78, 0) NOT NULL CHECK (value >= 0),
    signature     BYTEA NOT NULL,
    tx_hash       TEXT NOT NULL,
    log_index     INT NOT NULL,
    block_number  BIGINT NOT NULL,
    submitted_at  TIMESTAMPTZ NOT NULL,
    is_outlier    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, round_id, node_address),
    UNIQUE (chain_id, tx_hash, log_index),
    FOREIGN KEY (chain_id, round_id) REFERENCES oracle_rounds(chain_id, round_id)
);

-- decided_at and executed_at are separate for the same reason governance_proposals splits
-- dispatched from executed: a decision recorded but never executed is an operational alert, not a
-- resting state. The decision is written before the transaction so the audit trail survives the
-- transaction failing.
CREATE TABLE oracle_slashings (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    node_address  TEXT NOT NULL,
    round_id      NUMERIC(78, 0),
    amount        NUMERIC(78, 0) NOT NULL CHECK (amount > 0),
    reason        TEXT NOT NULL,
    decided_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    executed_at   TIMESTAMPTZ,
    tx_hash       TEXT,
    log_index     INT,
    block_number  BIGINT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- NULLs compare distinct in Postgres, so many undecided rows coexist while executed ones stay
    -- unique per event.
    UNIQUE (chain_id, tx_hash, log_index),
    FOREIGN KEY (chain_id, node_address) REFERENCES oracle_nodes(chain_id, address)
);

CREATE INDEX idx_oracle_feeds_active ON oracle_feeds(chain_id, active);
CREATE INDEX idx_oracle_nodes_active ON oracle_nodes(chain_id, active);
CREATE INDEX idx_oracle_rounds_feed_state ON oracle_rounds(chain_id, feed_id, state);
CREATE INDEX idx_oracle_rounds_block ON oracle_rounds(chain_id, block_number);
CREATE INDEX idx_oracle_submissions_round ON oracle_submissions(chain_id, round_id);
CREATE INDEX idx_oracle_submissions_node ON oracle_submissions(chain_id, node_address);
CREATE INDEX idx_oracle_slashings_node ON oracle_slashings(chain_id, node_address);
CREATE INDEX idx_oracle_slashings_unexecuted ON oracle_slashings(chain_id, decided_at)
    WHERE executed_at IS NULL;
