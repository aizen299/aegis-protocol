# Aegis Protocol

A production-grade modular blockchain protocol on Ethereum L2 (Arbitrum): a vault engine, a native
oracle network, DAO governance, and a zk privacy layer.

Architecture and conventions are locked in [`docs/`](docs/) — read
[`docs/project-spec.md`](docs/project-spec.md) first.

## Status

| Version | Module | State |
|---|---|---|
| v0.1 | Vault Engine | **Released** — tagged `v0.1.0` |
| v0.2 | Oracle Network | **Released** — tagged `v0.2.0` |
| v0.3 | DAO Governance | **In progress** — contracts, indexing, and endpoints done; role migration next |
| v0.4 | zk Privacy Layer | Scaffolded service, no circuits |
| v1.0 | Production Release | Not started |

No mainnet deployment and no real funds are in scope. Local Anvil through v0.4; Arbitrum Sepolia
for v1.0 staging.

### What is built

**v0.1 — Vault Engine.** UUPS vault with internal share accounting, one optional yield strategy, and
manager-configurable risk controls. Event indexer, REST API, Next.js dashboard.

**v0.2 — Oracle Network.** `OracleStaking` (registration, stake, unbonding, capped slashing with a
per-round guard) and `OracleRounds` (round lifecycle, EIP-712 submissions, on-chain medianization, a
fail-closed reader). Indexing for every event either emits, seven read endpoints, an aggregation
service that verifies signatures and medianizes independently, an executor that submits penalties,
and an oracle node that fetches from independent sources.

**v0.3 — DAO Governance.** `AegisToken` (fixed supply, timestamp-keyed vote checkpoints),
`Governor` (proposals, snapshotted voting, guardian cancellation), and `Timelock`, which holds the
queue, owns the delay, and performs the call — branching on the destination chain rather than
assuming a local target. Protocol roles are meant to sit on the timelock, not the governor, so a
governor can be replaced without migrating every role. The nine invariants in the plan are
enforced by a fuzzing handler, not just written down — they caught a one-second window in which a
proposal reported itself open for voting while every vote reverted. A proposal is created, voted,
queued, and executed against a real chain end to end. Proposals and votes are indexed into
Postgres and served over `/v1/governance`, with every raw weight accompanied by the scale that
makes it readable, and the whole path is exercised end to end against a real chain, a real
database, and the real API. Still to come: the role-migration procedure.

Current: **251 contract tests**, 12 backend packages, **29 end-to-end tests**, Slither clean.
[`docs/v0.2-oracle-plan.md`](docs/v0.2-oracle-plan.md) tracks the remaining v0.2 work and the design
decisions behind it.

## Layout

```
contracts/   Solidity + Foundry      settlement layer
backend/     Go                      indexing, aggregation, APIs
zk/          Rust + circuits         proof generation
frontend/    Next.js                 dashboards
infra/       Terraform               AWS
```

Each directory is versioned, built, and tested in isolation — CI triggers per path. The only
coupling permitted across them is contract ABIs, HTTP APIs, and event schemas.

## Quick start

```bash
cp .env.example .env
make dev                    # postgres, redis, anvil, migrations, indexer, api
make deploy-local           # v0.1 vault + a six-decimal test token
make deploy-oracle-local    # v0.2 oracle contracts
```

Each deploy writes an artifact under `contracts/deployments/` — `<chainId>.json` for the vault,
`oracle-<chainId>.json` for the oracle. Copy the printed addresses into `.env`
(`CONTRACT_VAULT_ENGINE`, and optionally `CONTRACT_ORACLE_ROUNDS`, `CONTRACT_ORACLE_STAKING`,
`CONTRACT_ORACLE_STAKE_TOKEN`), then `make dev` again to start indexing. The oracle contracts are
optional — leaving them unset indexes only the vault.

The local deploy targets sign with Anvil's first account and need no key configured. Deploying
anywhere else requires `PRIVATE_KEY` and uses `make deploy-vault`, which refuses to run without it
rather than defaulting to a well-known test key.

`APP_ENV` is required and has no default. See [Secrets](#secrets).

The API listens on **8090** — 8080 is deliberately avoided as it is commonly taken by Jenkins.

```bash
make test        # every layer, in isolation
make e2e         # real chain, real database, real indexer and API
make lint
make security    # slither
make help        # all targets
```

`make test` verifies each component against a mock of its neighbour. `make e2e` is the only thing
that exercises the seam between them, and it is where every defect this project has shipped was
caught: it boots Anvil and Postgres, deploys against a **six-decimal** token, moves value through
deposit and withdraw, runs a full oracle round with submissions signed by the same Go code a node
binary will use, indexes all of it, and asserts the APIs return the exact raw amounts.

Six decimals is deliberate. Eighteen is the value every layer would get right by accident.

## Prerequisites

Foundry, Go 1.24+, Rust 1.82+, Node 22+, Docker, and (for `make security`) Slither.

## Architecture in one paragraph

Contracts are the deterministic settlement layer, not storage. They emit indexed events; the Go
indexer is the *only* consumer of chain state and is cursor-based, idempotent, and reorg-safe. The
backend is where protocol intelligence lives. New contract functionality is always paired with a
well-designed event, because that event is the entire indexing surface.

## Values carry their scale

Every amount is stored and served raw — unscaled base units, as a JSON string, never a number,
because a `uint256` does not survive a `float64`. The scale travels beside it and is always read
from chain, never assumed:

| Value | Scale from |
|---|---|
| token amounts | the asset's `decimals()`, recorded in `assets` |
| vault shares | asset decimals plus the vault's `virtualSharesOffset()`, recorded in `vaults` |
| oracle values | the feed's decimals, declared at registration |
| node stake | the stake asset's decimals |

Foreign keys enforce it: an amount cannot be stored without the metadata that makes it readable, and
a token that will not report its decimals fails the batch rather than being defaulted. This exists
because assuming 18 is correct for every asset built so far and silently wrong by twelve orders of
magnitude for the first one that is not.

## Event identity

A chain event is identified by `(chain_id, tx_hash, log_index)`, never by `(chain_id, tx_hash)`. One
transaction routinely emits several instances of the same event, and because ingestion is idempotent
via `ON CONFLICT DO NOTHING`, a key omitting `log_index` silently discards all but the first — data
loss the indexer cannot detect. See `docs/database.md`.

## Secrets

`APP_ENV` is required and has no default. It selects where sensitive material comes from, and it is
declared rather than inferred: a process must never decide it is in development because a
development variable happens to be set.

| `APP_ENV` | Source |
|---|---|
| `local` | the environment variable named by `SLASHER_KEY_REF` |
| `staging`, `production` | AWS SSM Parameter Store, at that path |

There is no fallback in either direction. An unregistered provider, a failed fetch, an empty value,
or malformed material all fail startup. Outside `local` the environment provider is not even
registered, so two independent things would have to be wrong for a staging process to read a key out
of its environment.

The provider is logged; the material never is. Full policy in
[`docs/v0.2-oracle-plan.md`](docs/v0.2-oracle-plan.md) §2.7.

## Multi-chain readiness

Phase 1 is single-chain, but Solana is the confirmed Phase 2 target, and it is a rewrite of the
settlement layer rather than a port. Three constraints are binding from v0.1 so that adding it later
is additive:

1. Cross-layer identity is 32 bytes, not 20.
2. `backend/internal/chain` is an interface; `evm/` is one implementation behind it.
3. All chain-derived state carries `chain_id`, and uniqueness constraints include it.

Constraints 1 and 2 are enforced by tests in `backend/internal/architecture/`, which fail the build
if any package outside `internal/chain/evm` imports an EVM driver. That boundary has already
rejected one legitimate-looking change: EIP-712 signing and key generation are EVM-specific, so they
live behind it rather than widening it. Full reasoning, including the cross-chain messaging
analysis, is in `docs/project-spec.md` §7.

## Security posture

Treated as a production protocol, not a prototype. CEI plus `nonReentrant` on all value-transferring
external functions, `SafeERC20` only, no spot-price-based decisions, replay-resistant signed data,
no unbounded loops over user input. Slither runs in CI and fails on MEDIUM; the current suppression
set is justified in [`contracts/README.md`](contracts/README.md).

Two protocol-level constraints worth stating here:

- **Slashing is capped on-chain.** `SLASHER_ROLE` is a backend hot key, so the contract bounds each
  slash at 10% of remaining stake. The backend decides whether to slash; the contract decides what
  is survivable.
- **Stake unbonds over seven days.** Slashing is decided off-chain after a round settles, so without
  a delay a node could submit bad data, watch the round settle, and withdraw before the penalty
  landed.

### Upgrades

Contracts are UUPS with append-only storage. The released layout is committed at
`contracts/deployments/layouts/`, and `make contracts-layout-check` runs in CI, so a reordered or
removed variable fails the build rather than corrupting storage on a live deployment. An empty
layout counts as a failure, not a match: `forge inspect` returns nothing on a stale cache, and
comparing nothing to nothing would otherwise pass for a contract whose layout was never read.

## Keeping this file honest

`make docs-check` verifies the claims above — the test counts, that every version described as
released has a matching tag, and that every path linked here exists. It runs in CI on every push,
without a path filter: the README goes stale on changes that never touch it, so a check gated on
the README changing would miss exactly the cases that cause drift.

## Known gaps

[DEFERRED.md](DEFERRED.md) records what is deliberately unfinished, each with a trigger that will
actually fire. Anything without a real trigger is recorded as accepted risk rather than deferred
work, because a deferral nobody reaches is a decision to drop it.

## Licence

[MIT](LICENSE).
