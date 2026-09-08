# Aegis Protocol

A production-grade modular blockchain protocol on Ethereum L2 (Arbitrum): a vault engine, a native
oracle network, DAO governance, and a zk privacy layer.

Architecture and conventions are locked in [`docs/`](docs/) — read
[`docs/project-spec.md`](docs/project-spec.md) first.

## Status

| Version | Module | State |
|---|---|---|
| v0.1 | Vault Engine | **Implemented** — contracts, indexer, API, UI |
| v0.2 | Oracle Network | Not started |
| v0.3 | DAO Governance | Not started |
| v0.4 | zk Privacy Layer | Scaffolded service, no circuits |
| v1.0 | Production Release | Not started |

No mainnet deployment and no real funds are in scope. Local Anvil through v0.4; Arbitrum Sepolia
for v1.0 staging.

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
make dev                  # postgres, redis, anvil, migrations, indexer, api
make deploy-local         # deploy VaultEngine to anvil, prints the proxy address
```

Set `CONTRACT_VAULT_ENGINE` in `.env` to the printed proxy, then `make dev` again to start indexing.
The API listens on **8090** — 8080 is deliberately avoided as it is commonly taken by Jenkins.

```bash
make test                 # every layer, in isolation
make e2e                  # real chain, real database, real indexer and API
make lint
make security             # slither
make help                 # all targets
```

`make test` verifies each component against a mock of its neighbour. `make e2e` is the only thing
that exercises the seam between them: it boots Anvil and Postgres, deploys the vault against a
**six-decimal** token, moves real value through deposit and withdraw, indexes it, and asserts the
API returns the exact raw amounts. Six decimals is deliberate — eighteen is the value every layer
would get right by accident.

## Prerequisites

Foundry, Go 1.24+, Rust 1.82+, Node 22+, Docker, and (for `make security`) Slither.

## Architecture in one paragraph

Contracts are the deterministic settlement layer, not storage. They emit indexed events; the Go
indexer is the *only* consumer of chain state and is cursor-based, idempotent, and reorg-safe. The
backend is where protocol intelligence lives. New contract functionality is always paired with a
well-designed event, because that event is the entire indexing surface.

## Multi-chain readiness

Phase 1 is single-chain, but Solana is the confirmed Phase 2 target, and it is a rewrite of the
settlement layer rather than a port. Three constraints are binding from v0.1 so that adding it later
is additive:

1. Cross-layer identity is 32 bytes, not 20.
2. `backend/internal/chain` is an interface; `evm/` is one implementation behind it.
3. All chain-derived state carries `chain_id`, and uniqueness constraints include it.

Constraints 1 and 2 are enforced by tests in `backend/internal/architecture/`, which fail the build
if any package outside `internal/chain/evm` imports an EVM driver. Full reasoning, including the
cross-chain messaging analysis, is in `docs/project-spec.md` §7.

## Security posture

Treated as a production protocol, not a prototype. CEI plus `nonReentrant` on all value-transferring
external functions, `SafeERC20` only, no spot-price-based decisions, replay-resistant signed data,
no unbounded loops over user input. Slither runs in CI and fails on MEDIUM; the current suppression
set is justified in [`contracts/README.md`](contracts/README.md).

## Known gaps

[DEFERRED.md](DEFERRED.md) records what is deliberately unfinished, each with a trigger that will
actually fire. Anything without a real trigger is recorded as accepted risk rather than deferred
work, because a deferral nobody reaches is a decision to drop it.

## Licence

[MIT](LICENSE).
