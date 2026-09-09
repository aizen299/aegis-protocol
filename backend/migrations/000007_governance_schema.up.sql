-- v0.3 schema: governance proposals and votes.
--
-- Four deviations from the governance block in docs/database.md, each for a reason v0.2 already
-- established rather than a new preference:
--
--   * Vote weights are NUMERIC(78,0), not NUMERIC(38,18). A weight is a uint256 token amount, and
--     the narrower column cannot hold every legal value. An indexer that cannot store a valid
--     on-chain value stalls, so this is a correctness problem, not a style choice — the same
--     argument recorded at the top of 000002.
--
--   * governance_proposals carries log_index and is unique on (chain_id, tx_hash, log_index) as
--     well as on the semantic key. The Event Identity section of docs/database.md requires this of
--     every table holding indexed events; its own example for this table omitted it.
--
--   * A `governors` table gives every weight a discoverable scale, completing the pattern assets,
--     oracle_feeds, and vaults already follow. A weight without its scale is uninterpretable, and
--     the foreign key means one cannot be written without it.
--
--   * operation_id and executable_at are stored. The timelock is only useful if a passed proposal
--     is observable before it takes effect, which means the API has to be able to say when a
--     queued proposal becomes executable.
--
-- UNIQUE (chain_id, proposal_id) is kept as locked. It encodes one governor per chain, the same
-- assumption oracle_rounds and vaults already make about their contracts.

CREATE TABLE governors (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id         BIGINT NOT NULL,
    address          TEXT NOT NULL,
    token_address    TEXT NOT NULL,
    timelock_address TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, address),
    FOREIGN KEY (chain_id, token_address) REFERENCES assets(chain_id, address)
);

CREATE TRIGGER governors_set_updated_at
    BEFORE UPDATE ON governors
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE governance_proposals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id        BIGINT NOT NULL,
    governor_address TEXT NOT NULL,
    proposal_id     NUMERIC(78, 0) NOT NULL CHECK (proposal_id > 0),
    proposer        TEXT NOT NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    -- The action, as emitted. ProposalCreated carries all of it so reconstructing what a proposal
    -- does never needs a contract read.
    target          TEXT NOT NULL,
    -- Destination chain of the action. Equals chain_id for a local proposal, which is the only
    -- case reachable in Phase 1. See project-spec.md §7 — the executor must not assume a local
    -- target, and neither may the schema.
    target_chain_id BIGINT NOT NULL,
    action_value    NUMERIC(78, 0) NOT NULL DEFAULT 0 CHECK (action_value >= 0),
    calldata        BYTEA NOT NULL,
    state           TEXT NOT NULL DEFAULT 'pending'
                    CHECK (state IN ('pending', 'active', 'succeeded', 'defeated', 'queued',
                                     'dispatched', 'executed', 'failed', 'cancelled')),
    vote_start      BIGINT NOT NULL,
    vote_end        BIGINT NOT NULL,
    votes_for       NUMERIC(78, 0) NOT NULL DEFAULT 0 CHECK (votes_for >= 0),
    votes_against   NUMERIC(78, 0) NOT NULL DEFAULT 0 CHECK (votes_against >= 0),
    votes_abstain   NUMERIC(78, 0) NOT NULL DEFAULT 0 CHECK (votes_abstain >= 0),
    -- The timelock operation this proposal was queued into, and when it becomes executable.
    operation_id    NUMERIC(78, 0) CHECK (operation_id IS NULL OR operation_id > 0),
    executable_at   TIMESTAMPTZ,
    queued_at       TIMESTAMPTZ,
    dispatched_at   TIMESTAMPTZ,
    executed_at     TIMESTAMPTZ,
    cancelled_at    TIMESTAMPTZ,
    tx_hash         TEXT NOT NULL,
    log_index       INT NOT NULL,
    block_number    BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, proposal_id),
    UNIQUE (chain_id, tx_hash, log_index),
    FOREIGN KEY (chain_id, governor_address) REFERENCES governors(chain_id, address)
);

CREATE TRIGGER governance_proposals_set_updated_at
    BEFORE UPDATE ON governance_proposals
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE governance_votes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    proposal_id   NUMERIC(78, 0) NOT NULL,
    voter         TEXT NOT NULL,
    support       SMALLINT NOT NULL CHECK (support IN (0, 1, 2)), -- 0=against, 1=for, 2=abstain
    weight        NUMERIC(78, 0) NOT NULL CHECK (weight > 0),
    reason        TEXT,
    tx_hash       TEXT NOT NULL,
    log_index     INT NOT NULL,
    block_number  BIGINT NOT NULL,
    voted_at      TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (chain_id, proposal_id) REFERENCES governance_proposals(chain_id, proposal_id),
    -- The semantic rule and idempotent ingestion are different constraints and both are declared.
    UNIQUE (chain_id, tx_hash, log_index),
    UNIQUE (chain_id, proposal_id, voter)
);

CREATE INDEX idx_governance_proposals_state ON governance_proposals(chain_id, state, vote_end DESC);
CREATE INDEX idx_governance_proposals_governor ON governance_proposals(chain_id, governor_address);
-- Serves the operational question the timelock exists to make answerable: what is queued, and when
-- does it become executable.
CREATE INDEX idx_governance_proposals_queued ON governance_proposals(chain_id, executable_at)
    WHERE state = 'queued';
CREATE INDEX idx_governance_votes_proposal ON governance_votes(chain_id, proposal_id);
CREATE INDEX idx_governance_votes_voter ON governance_votes(chain_id, voter);
