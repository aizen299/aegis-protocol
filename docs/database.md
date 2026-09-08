# Database Domain Reference — Project Blockchain

## Technology Decisions

- **PostgreSQL** (via AWS RDS): source of truth for all persistent state.
- **Redis** (via AWS ElastiCache): hot-path cache, pub/sub for WebSocket fanout.
- **Migrations**: `golang-migrate` with numbered SQL files. Up + down migrations required.
- **Driver**: `pgx/v5` with connection pooling via `pgxpool`.

## Connection Pool Configuration

```go
config, err := pgxpool.ParseConfig(dsn)
config.MaxConns = 25
config.MinConns = 5
config.MaxConnLifetime = 5 * time.Minute
config.MaxConnIdleTime = 1 * time.Minute
config.HealthCheckPeriod = 30 * time.Second
```

## Schema Design Principles

- Use `uuid` (gen_random_uuid()) as primary key for all entities exposed externally.
- Use `bigserial` for internal sequence/ordering where UUID is unnecessary overhead.
- All tables: `created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()` and `updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`.
- `updated_at` managed via trigger, not application code.
- Soft deletes via `deleted_at TIMESTAMPTZ` where audit trail matters. Hard delete otherwise.
- Foreign keys with `ON DELETE RESTRICT` unless cascade is explicitly required.
- Never FLOAT for any monetary value.
- **Token amounts are stored raw, in the asset's own base units: `NUMERIC(78, 0)`, unscaled.**
  Decimals are a property of the asset, not of the protocol, and live in the `assets` table. Scaling
  on ingest bakes one token's decimals into every row and misrepresents by twelve orders of
  magnitude any asset that does not match — a six-decimal USDC amount written as though it were
  eighteen-decimal. Callers scale once, at the presentation edge, using `assets.decimals`.
- **Oracle values are raw too.** An earlier revision of this document made them an exception,
  stored as `NUMERIC(38, 18)` on the grounds that the contract fixes the scale at 18 so it is
  contractual rather than a property of any token. That reasoning holds for *where the scale comes
  from* and does not justify a narrower column: `NUMERIC(38, 18)` cannot represent every `uint256` a
  node may legitimately submit, and an indexer that cannot store a valid on-chain value stalls. The
  scale still differs in origin — it lives on `oracle_feeds.decimals` rather than on an asset — but
  the storage rule is the same everywhere: raw base units, scale recorded alongside.

## Multi-Chain Readiness

Phase 1 is single-chain (Arbitrum), but Solana is the confirmed Phase 2 target — see
`project-spec.md` §7. Three schema rules are binding from v0.1 so that adding a second chain is an
additive migration rather than a rewrite of every constraint and index.

**1. Identity columns hold 32 bytes, not 20.** An EVM address is 20 bytes; a Solana pubkey is
32. Identity columns (`user_address`, `node_address`, `proposer`, `voter`, `target`,
`asset_address`) are `TEXT` and store the chain's canonical string encoding — `0x`-prefixed hex
for EVM, base58 for Solana. Do not narrow these to `BYTEA(20)` or add a `CHECK` on hex length.
Because encodings differ per chain, an identity string is only meaningful alongside its
`chain_id`.

**2. Every chain-derived row carries `chain_id`, and uniqueness includes it.** A transaction hash
is unique within a chain, not across chains; the same is true of round IDs, proposal IDs, and
node addresses. Every `UNIQUE` constraint and every foreign key on chain-derived data is
therefore composite with `chain_id`. `chain_id` is `BIGINT` — EVM chain IDs are numeric, and
non-EVM chains are assigned internal IDs from a reserved high range recorded in
`pkg/types`.

This does not apply to purely off-chain tables (application users, API keys, job state), which
have no `chain_id`.

Asset metadata is chain-scoped for the same reason: the same symbol on two chains is two different
tokens, and decimals can differ between a token's deployments.

**3. Governance proposals record a destination, and their state machine is asynchronous.**
Governance is the only sanctioned cross-chain message (`project-spec.md` §7), so
`governance_proposals` carries `target_chain_id` distinct from `chain_id` — the chain the
proposal *lives* on versus the chain its action *executes* on. In Phase 1 they are always equal.

Because a remote action is dispatched rather than executed, `'executed'` is not reachable
synchronously for one, and the state machine separates the two outcomes:

```
queued ──> executed              (target_chain_id = chain_id; local call succeeded)
queued ──> dispatched ──> executed   (remote; execution confirmed on the destination chain)
                      └─> failed     (remote; message expired, reverted, or was never delivered)
```

`dispatched` means "handed to the messaging layer, outcome unknown" and is **not** terminal — the
indexer resolves it only on confirmation from the destination chain. `dispatched_at` and
`executed_at` are recorded separately so the latency between them is measurable, since an
unresolved `dispatched` proposal is an operational alert rather than a normal steady state.

In Phase 1 the `dispatched` and `failed` states are unreachable. They exist so that adding them
later is not a migration of the governance state machine — see the upgrade-cost argument in §7.

## Event Identity

**A chain event is identified by `(chain_id, tx_hash, log_index)`, never by `(chain_id, tx_hash)`.**

One transaction routinely emits several instances of the same event: a router depositing on behalf
of multiple users, a batch voter casting on several proposals, or two vaults touched in one call.
Because the indexer writes with `ON CONFLICT DO NOTHING`, a uniqueness constraint that omits
`log_index` silently discards every event after the first in a transaction — data loss the indexer
cannot detect and no error surfaces for.

`log_index` is therefore a column on every table holding indexed events, and is part of the
uniqueness constraint wherever the transaction hash is. Where a table has a natural semantic key as
well (`(chain_id, proposal_id, voter)` for votes, `(chain_id, round_id, node_address)` for oracle
submissions), both constraints are declared: the semantic key expresses the protocol rule, the
event key expresses idempotent ingestion.

## Core Schema

```sql
-- Oracle Submissions
CREATE TABLE oracle_rounds (
    id            BIGSERIAL PRIMARY KEY,
    chain_id      BIGINT NOT NULL,
    round_id      NUMERIC(78, 0) NOT NULL,
    feed_id       TEXT NOT NULL,
    started_at    TIMESTAMPTZ NOT NULL,
    settled_at    TIMESTAMPTZ,
    aggregated_value NUMERIC(38, 18),
    submission_count INT NOT NULL DEFAULT 0,
    state         TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'quorum_met', 'settled', 'failed')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, round_id)
);

CREATE TABLE oracle_submissions (
    id            BIGSERIAL PRIMARY KEY,
    chain_id      BIGINT NOT NULL,
    round_id      NUMERIC(78, 0) NOT NULL,
    node_address  TEXT NOT NULL,
    value         NUMERIC(38, 18) NOT NULL,
    signature     BYTEA NOT NULL,
    tx_hash       TEXT,
    block_number  BIGINT,
    submitted_at  TIMESTAMPTZ NOT NULL,
    is_outlier    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (chain_id, round_id) REFERENCES oracle_rounds(chain_id, round_id),
    UNIQUE (chain_id, round_id, node_address)
);

-- Asset Metadata
-- Decimals are what make a raw amount interpretable. Every table holding token amounts carries a
-- foreign key here, so an amount cannot exist without its scale.
CREATE TABLE assets (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    address       TEXT NOT NULL,
    decimals      SMALLINT NOT NULL CHECK (decimals >= 0 AND decimals <= 38),
    symbol        TEXT,
    name          TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, address)
);

-- Vault State
-- amount and shares are raw uint256 base units, unscaled. See "Token amounts" above.
CREATE TABLE vault_deposits (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    user_address  TEXT NOT NULL,
    asset_address TEXT NOT NULL,
    vault_address TEXT NOT NULL,
    amount        NUMERIC(78, 0) NOT NULL CHECK (amount >= 0),
    shares        NUMERIC(78, 0) NOT NULL CHECK (shares >= 0),
    tx_hash       TEXT NOT NULL,
    log_index     INT NOT NULL,
    block_number  BIGINT NOT NULL,
    deposited_at  TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, tx_hash, log_index),
    FOREIGN KEY (chain_id, asset_address) REFERENCES assets(chain_id, address)
);

CREATE TABLE vault_withdrawals (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    user_address  TEXT NOT NULL,
    asset_address TEXT NOT NULL,
    vault_address TEXT NOT NULL,
    amount        NUMERIC(78, 0) NOT NULL CHECK (amount >= 0),
    shares        NUMERIC(78, 0) NOT NULL CHECK (shares >= 0),
    tx_hash       TEXT NOT NULL,
    log_index     INT NOT NULL,
    block_number  BIGINT NOT NULL,
    withdrawn_at  TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, tx_hash, log_index),
    FOREIGN KEY (chain_id, asset_address) REFERENCES assets(chain_id, address)
);

-- Governance
CREATE TABLE governance_proposals (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    proposal_id   NUMERIC(78, 0) NOT NULL,
    proposer      TEXT NOT NULL,
    title         TEXT NOT NULL,
    description   TEXT,
    calldata      BYTEA NOT NULL,
    target        TEXT NOT NULL,
    -- Destination chain of the proposal's action. Equals chain_id for a local proposal, which is
    -- the only case reachable in Phase 1. See project-spec.md §7 — the executor must not assume a
    -- local target.
    target_chain_id BIGINT NOT NULL,
    state         TEXT NOT NULL DEFAULT 'pending'
                  CHECK (state IN ('pending', 'active', 'succeeded', 'defeated', 'queued',
                                   'dispatched', 'executed', 'failed', 'cancelled')),
    vote_start    BIGINT NOT NULL,
    vote_end      BIGINT NOT NULL,
    votes_for     NUMERIC(38, 18) NOT NULL DEFAULT 0,
    votes_against NUMERIC(38, 18) NOT NULL DEFAULT 0,
    votes_abstain NUMERIC(38, 18) NOT NULL DEFAULT 0,
    dispatched_at TIMESTAMPTZ,
    executed_at   TIMESTAMPTZ,
    tx_hash       TEXT NOT NULL,
    block_number  BIGINT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, proposal_id)
);

CREATE TABLE governance_votes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    proposal_id   NUMERIC(78, 0) NOT NULL,
    voter         TEXT NOT NULL,
    support       SMALLINT NOT NULL CHECK (support IN (0, 1, 2)), -- 0=against, 1=for, 2=abstain
    weight        NUMERIC(38, 18) NOT NULL,
    reason        TEXT,
    tx_hash       TEXT NOT NULL,
    log_index     INT NOT NULL,
    block_number  BIGINT NOT NULL,
    voted_at      TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (chain_id, proposal_id) REFERENCES governance_proposals(chain_id, proposal_id),
    UNIQUE (chain_id, tx_hash, log_index),
    UNIQUE (chain_id, proposal_id, voter)
);

-- Indexer State
CREATE TABLE indexer_cursors (
    id            BIGSERIAL PRIMARY KEY,
    service_name  TEXT NOT NULL,
    chain_id      BIGINT NOT NULL,
    last_block    BIGINT NOT NULL DEFAULT 0,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (service_name, chain_id)
);

-- Node Registry
CREATE TABLE oracle_nodes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chain_id      BIGINT NOT NULL,
    address       TEXT NOT NULL,
    staked_amount NUMERIC(38, 18) NOT NULL DEFAULT 0,
    slashed_total NUMERIC(38, 18) NOT NULL DEFAULT 0,
    missed_rounds INT NOT NULL DEFAULT 0,
    active        BOOLEAN NOT NULL DEFAULT TRUE,
    registered_at TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (chain_id, address)
);
```

## Indexes

```sql
-- chain_id leads every index on chain-derived data: queries are always chain-scoped, and a
-- leading chain_id keeps them selective once a second chain is indexed.

-- Oracle
CREATE INDEX idx_oracle_submissions_round ON oracle_submissions(chain_id, round_id);
CREATE INDEX idx_oracle_submissions_node ON oracle_submissions(chain_id, node_address);
CREATE INDEX idx_oracle_rounds_feed_state ON oracle_rounds(chain_id, feed_id, state);

-- Vault
CREATE INDEX idx_vault_deposits_user ON vault_deposits(chain_id, user_address);
CREATE INDEX idx_vault_deposits_block ON vault_deposits(chain_id, block_number);
CREATE INDEX idx_vault_deposits_vault ON vault_deposits(chain_id, vault_address);
CREATE INDEX idx_vault_withdrawals_user ON vault_withdrawals(chain_id, user_address);
CREATE INDEX idx_vault_withdrawals_block ON vault_withdrawals(chain_id, block_number);
CREATE INDEX idx_vault_withdrawals_vault ON vault_withdrawals(chain_id, vault_address);

-- Governance
CREATE INDEX idx_governance_proposals_state ON governance_proposals(chain_id, state);
CREATE INDEX idx_governance_votes_proposal ON governance_votes(chain_id, proposal_id);
CREATE INDEX idx_governance_votes_voter ON governance_votes(chain_id, voter);
```

## Redis Key Schema

Keys for chain-derived data are chain-scoped, for the same reason the tables are: an address or
round ID is only meaningful alongside its chain. The namespace is
`pb:{module}:{chain_id}:{entity}:{id}`.

Cached amounts are raw base units, exactly as stored. Every cached payload carrying an amount
carries its `decimals` too, so a cache hit is as interpretable as a database read.

```
pb:oracle:{chain_id}:latest:{feed_id}          → JSON: {value, roundId, settledAt, submissionCount}  TTL: 30s
pb:vault:{chain_id}:position:{address}         → JSON: {shares, depositedTotal, withdrawnTotal,
                                                        decimals, shareDecimals, lastDepositAt}      TTL: 60s
pb:vault:{chain_id}:tvl:{vault_address}        → JSON: {amount, decimals, asset}                     TTL: 30s
pb:governance:{chain_id}:proposal:{proposalId} → JSON: proposal object                               TTL: 300s
pb:governance:{chain_id}:proposals:active      → JSON array of active proposal IDs                   TTL: 60s
pb:node:{chain_id}:info:{address}              → JSON: NodeInfo                                      TTL: 120s
```

## Migration Conventions

```
migrations/
├── 000001_init_schema.up.sql
├── 000001_init_schema.down.sql
├── 000002_add_oracle_nodes.up.sql
├── 000002_add_oracle_nodes.down.sql
```

Run via:
```bash
migrate -path migrations/ -database $DB_DSN up
```

Applied automatically in container entrypoint before service starts.

## Query Patterns

### Pagination
```go
// Always paginate. Never SELECT * without LIMIT.
// Every query over chain-derived data filters on chain_id — see Multi-Chain Readiness.
const q = `
    SELECT id, round_id, feed_id, aggregated_value, settled_at
    FROM oracle_rounds
    WHERE chain_id = $1 AND feed_id = $2 AND state = 'settled'
    ORDER BY round_id DESC
    LIMIT $3 OFFSET $4
`
```

### Upsert (Idempotent Indexing)
```go
// The conflict target includes log_index. Without it, a transaction carrying two Deposited events
// silently loses the second — see "Event Identity".
const q = `
    INSERT INTO vault_deposits
        (chain_id, user_address, asset_address, vault_address, amount, shares, tx_hash, log_index, block_number, deposited_at)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
    ON CONFLICT (chain_id, tx_hash, log_index) DO NOTHING
`
```

### Reading amounts
```go
// Amounts come back as raw base units. Join the asset to get the scale; never assume 18.
const q = `
    SELECT d.amount, d.shares, a.decimals, a.symbol
    FROM vault_deposits d
    JOIN assets a ON a.chain_id = d.chain_id AND a.address = d.asset_address
    WHERE d.chain_id = $1 AND d.user_address = $2
    ORDER BY d.block_number DESC, d.log_index DESC
    LIMIT $3 OFFSET $4
`
```

### Transaction Example
```go
tx, err := pool.Begin(ctx)
if err != nil { return err }
defer tx.Rollback(ctx)

// ... execute multiple queries on tx ...

return tx.Commit(ctx)
```
