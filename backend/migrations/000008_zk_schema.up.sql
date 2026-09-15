-- v0.4 schema: the commitment set and the private actions spent against it.
--
-- A deliberate omission runs through this file: nothing here records who did anything.
--
-- The chain already reveals each transaction's sender, so storing it would leak nothing new in
-- principle. But a table that puts an inserter beside a spender turns correlation from an exercise
-- in log archaeology into a single join, and the anonymity this module exists to provide is
-- exactly the difficulty of making that link. The indexer therefore stores what the events carry
-- and no more. If a future feature needs a sender, it belongs in its own table with that trade-off
-- argued explicitly, not added here as a convenience column.

CREATE TABLE zk_gates (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id         BIGINT NOT NULL,
    address          TEXT NOT NULL,
    tree_address     TEXT NOT NULL,
    verifier_address TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, address)
);

CREATE TRIGGER zk_gates_set_updated_at
    BEFORE UPDATE ON zk_gates
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE zk_commitments (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    tree_address  TEXT NOT NULL,
    -- The tree is append-only, so the index is stable and is what a prover needs to build a path.
    leaf_index    BIGINT NOT NULL CHECK (leaf_index >= 0),
    commitment    TEXT NOT NULL,
    -- The root the tree held after this insert. Storing it lets the mirror be checked against the
    -- chain leaf by leaf rather than only at the tip, which is how a silent divergence is found.
    root_after    TEXT NOT NULL,
    tx_hash       TEXT NOT NULL,
    log_index     INT NOT NULL,
    block_number  BIGINT NOT NULL,
    inserted_at   TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, tree_address, leaf_index),
    UNIQUE (chain_id, tree_address, commitment),
    UNIQUE (chain_id, tx_hash, log_index)
);

CREATE TABLE zk_actions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    gate_address  TEXT NOT NULL,
    action_id     TEXT NOT NULL,
    name          TEXT NOT NULL,
    registered    BOOLEAN NOT NULL DEFAULT TRUE,
    tx_hash       TEXT NOT NULL,
    log_index     INT NOT NULL,
    block_number  BIGINT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, gate_address, action_id),
    FOREIGN KEY (chain_id, gate_address) REFERENCES zk_gates(chain_id, address)
);

CREATE TRIGGER zk_actions_set_updated_at
    BEFORE UPDATE ON zk_actions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE zk_private_actions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    gate_address  TEXT NOT NULL,
    nullifier     TEXT NOT NULL,
    action_id     TEXT NOT NULL,
    -- Which root the proof was checked against. Useful for diagnosing a proof that failed because
    -- the tree moved, and it reveals nothing: the root is public and shared by every member.
    root          TEXT NOT NULL,
    tx_hash       TEXT NOT NULL,
    log_index     INT NOT NULL,
    block_number  BIGINT NOT NULL,
    executed_at   TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Mirrors the on-chain rule: a nullifier is spent at most once per chain and gate.
    UNIQUE (chain_id, gate_address, nullifier),
    UNIQUE (chain_id, tx_hash, log_index),
    FOREIGN KEY (chain_id, gate_address) REFERENCES zk_gates(chain_id, address)
);

-- Serves the prover: every leaf of a tree in insertion order is what a Merkle path is built from.
CREATE INDEX idx_zk_commitments_tree ON zk_commitments(chain_id, tree_address, leaf_index);
-- Serves the anonymity-set question the plan's risk register commits to surfacing.
CREATE INDEX idx_zk_commitments_block ON zk_commitments(chain_id, tree_address, block_number DESC);
CREATE INDEX idx_zk_private_actions_gate ON zk_private_actions(chain_id, gate_address, executed_at DESC);
CREATE INDEX idx_zk_private_actions_action ON zk_private_actions(chain_id, gate_address, action_id);
