# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository state

This repo is currently **pre-development**. Only `docs/` exists (architecture/spec references) — there is no `contracts/`, `backend/`, `zk/`, `frontend/`, or `infra/` code yet, no `docker-compose.yml`, no `Makefile`, and no commits. The docs below are the locked spec this project must be built to; do not deviate from the tech stack or architecture they describe without an explicit instruction from the user.

Read `docs/project-spec.md` first for the overall plan and version roadmap (v0.1 Vault → v0.2 Oracle → v0.3 DAO Governance → v0.4 zk Privacy → v1.0 Production). The other docs are domain references for each layer:
- `docs/solidity.md` — contracts layer conventions
- `docs/go-backend.md` — backend service conventions
- `docs/oracle.md` — oracle network design (on-chain + off-chain)
- `docs/zk.md` — zk proof service and verifier design
- `docs/database.md` — Postgres schema and Redis key conventions
- `docs/devops.md` — Docker/AWS/Terraform/CI conventions

## Planned repository structure

```
project-blockchain/
├── contracts/          # Solidity + Foundry
├── backend/            # Go services (indexer, oracle aggregator, API)
├── zk/                 # Rust proof service + circuits (Circom/Noir)
├── frontend/           # Next.js
├── infra/              # Terraform
├── .github/workflows/
├── docker-compose.yml
└── Makefile
```

`Makefile` targets (per `docs/devops.md`): `make dev`, `make test`, `make build`, `make deploy-staging`. These don't exist yet — create them when scaffolding the corresponding layer.

## Commands (once each layer is scaffolded)

**Contracts** (`contracts/`, Foundry):
```bash
forge build --sizes
forge test -vvv
forge test --match-test testFuzz_deposit   # single test
forge coverage --report lcov
forge inspect <Contract> storage-layout    # required before any UUPS upgrade
slither src/ --config-file slither.config.json
```

**Backend** (`backend/`, Go):
```bash
go build ./...
go test ./... -race -count=1
go test ./internal/oracle/... -run TestMedianize -v   # single test
abigen --abi=out/VaultEngine.sol/VaultEngine.json --pkg=vaultengine --out=pkg/contracts/vault_engine.go
migrate -path migrations/ -database $DB_DSN up
```

**zk service** (`zk/`, Rust):
```bash
cargo build --release
cargo test
```

## Architecture

**Layering** (from `docs/project-spec.md` §10 — this is the core mental model): blockchain contracts are the deterministic settlement layer, not storage; the Go backend is the intelligence/coordination layer; the zk service adds privacy/verification; the oracle system is the trust-minimized bridge to off-chain data. Don't push logic into contracts that belongs in the backend, or vice versa — events are the contract between them.

**Event-driven integration**: Solidity contracts emit indexed events for every state change; the Go indexer is the *only* consumer of chain state and is cursor-based (persists last processed block to Postgres), idempotent (upsert/`ON CONFLICT DO NOTHING`), and reorg-safe (only processes up to `head - confirmBlocks`). New contract functionality must always be paired with a well-designed event, since that event is the entire indexing surface — see `docs/solidity.md` "Events" and `docs/go-backend.md` "Blockchain Event Indexer".

**Oracle data flow**: off-chain sources → oracle node (Docker container, signs with EIP-712) → `OracleModule.sol` (verifies stake, stores submission, tracks round state OPEN → QUORUM_MET → SETTLED) → Go aggregation service (listens for `SubmissionReceived`, verifies signatures off-chain, computes medianized value, detects outliers, writes to Postgres/Redis) → slashing executed on-chain by the backend-held `SLASHER_ROLE` key. Full detail in `docs/oracle.md`.

**zk flow**: Next.js client sends private inputs to the Rust proof service (never logged, zeroed after use) → service generates a proof against a compiled circuit → Go backend submits `(proof, publicInputs)` on-chain → `ZkVerifier.sol`/`ZkVaultGate` verifies and checks a nullifier map to prevent replay. Verifier contracts are always generated (snarkjs/Nargo), never hand-written. Full detail in `docs/zk.md`.

**Contracts are UUPS-upgradeable** (`docs/solidity.md`): `initialize()` not `constructor()`, `_authorizeUpgrade` gated by `UPGRADER_ROLE`, storage layout append-only (verify with `forge inspect` before any upgrade). Access control is role-based (`AccessControlUpgradeable`), never `Ownable`, with `DEFAULT_ADMIN_ROLE` expected to be a multisig in production.

**Backend data access**: `pgx/v5` directly, no ORM; queries are parameterized constants; Redis keys are namespaced `pb:{module}:{chain_id}:{entity}:{id}` (chain-derived data) or `pb:{module}:{entity}:{id}` (off-chain only) and always TTL'd (cache-aside, read Postgres on miss, `SETNX` to prevent thundering herd). Never cache authoritative decisions like slashing.

**Multi-chain readiness**: Phase 1 is single-chain (Arbitrum), but Solana is the confirmed Phase 2 target (`docs/project-spec.md` §7). Three constraints are binding from v0.1: cross-layer identity is 32 bytes not 20 (no `common.Address` in `pkg/types` or shared schemas), `internal/chain` is an interface with an `evm/` implementation behind it (indexer/oracle/vault never import `ethclient`), and every chain-derived row carries `chain_id` with all uniqueness constraints, foreign keys, and indexes composite on it. Details in `docs/go-backend.md` and `docs/database.md`, both "Multi-Chain Readiness".

**Monorepo boundary rule**: each top-level directory (`contracts/`, `backend/`, `zk/`, `frontend/`, `infra/`) is independently versioned and built/tested in isolation (see per-path CI triggers in `docs/devops.md`'s GitHub Actions examples) — avoid cross-directory imports or coupling outside of well-defined contract ABIs / HTTP APIs / event schemas.

## Security posture

This is treated as a production-grade protocol, not a prototype (`docs/project-spec.md` §14). Concretely: CEI + `nonReentrant` on all value-transferring external functions, `SafeERC20` only (never raw `.transfer()`/`.approve()`), no spot-price-based decisions (TWAP/medianization only), signed data must include chainId/nonce/contract address to prevent replay, and no unbounded loops over user-supplied arrays. Run Slither before considering any contract complete; HIGH/MEDIUM findings must be resolved, LOW findings need documented justification to skip. See the vulnerability tables in `docs/solidity.md`, `docs/oracle.md`, and `docs/zk.md` for the specific risks tracked per layer.
