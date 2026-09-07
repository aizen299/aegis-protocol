---
name: blockchain-dev
description: Scaffold new modules, run tests/static analysis, and check code against the locked conventions for Project Blockchain (contracts/, backend/, zk/). Use when the user asks to create/scaffold a new contract, Go service, or circuit; run forge/go/cargo tests or Slither; or review/check code for spec compliance (UUPS pattern, AccessControl, CEI, SafeERC20, event design, indexer idempotency, pgx query style, etc).
---

# Blockchain Dev

Project Blockchain is a locked-spec monorepo (see `docs/project-spec.md` and the per-layer docs: `docs/solidity.md`, `docs/go-backend.md`, `docs/oracle.md`, `docs/zk.md`, `docs/database.md`, `docs/devops.md`). This skill covers three related jobs. Read the relevant doc(s) before acting — conventions live there, not duplicated here.

Figure out which mode(s) the user's request needs — a single ask may span more than one.

## 1. Scaffold a new module

When asked to create/scaffold a new contract, Go service, or zk circuit:

1. Read the relevant doc(s) first (`docs/solidity.md` for contracts, `docs/go-backend.md` for backend, `docs/zk.md` for circuits/proof service) to pick up the current layout and required patterns — don't rely on memory of this file.
2. Match the existing directory layout exactly as documented (e.g. `contracts/src/<module>/`, `contracts/test/{unit,integration,fuzz,invariant}/`, `backend/internal/<module>/`, `backend/cmd/<binary>/`).
3. For Solidity: use the UUPS pattern (`initialize()` not constructor, `_authorizeUpgrade` gated by `UPGRADER_ROLE`, `AccessControlUpgradeable` not `Ownable`), CEI + `nonReentrant` on state-changing external functions, `SafeERC20` for token transfers, and emit an indexed event for every state change (past-tense names). Generate matching test skeletons in `test/unit/`, `test/fuzz/`, `test/invariant/` per `docs/solidity.md`'s testing requirements section.
4. For Go: typed env-driven config, `zerolog` structured logging, `context.Context` as first arg on blocking calls, `pgx/v5` with parameterized queries (no ORM), Redis keys namespaced `pb:{module}:{entity}:{id}` with a TTL.
5. For zk: put circuit source under `zk/circuits/`, Rust service code under `zk/src/`, never hand-write verifier contracts (generate via snarkjs/Nargo), never log private inputs.
6. After scaffolding, tell the user what was created and what's still a stub (e.g. "aggregation logic in `X.go` is a TODO — fill in per docs/oracle.md's medianization section").

## 2. Run tests / static analysis

- Contracts (`contracts/`): `forge build --sizes`, `forge test -vvv` (or `-vvv --match-test <name>` / `--match-contract <name>` for a single test), `forge coverage --report lcov`, `slither src/ --config-file slither.config.json`. Before reporting a UUPS-upgradeable contract change as done, also run `forge inspect <Contract> storage-layout` and confirm it's append-only vs. the prior layout.
- Backend (`backend/`): `go build ./...`, `go test ./... -race -count=1` (or `go test ./internal/<pkg>/... -run <TestName> -v` for a single test).
- zk (`zk/`): `cargo build --release`, `cargo test`.
- Summarize failures concisely; for Slither, call out unresolved HIGH/MEDIUM findings explicitly since those block completion per `docs/solidity.md` — LOW findings need a documented justification to skip, not silent suppression.
- If a layer hasn't been scaffolded yet (no `contracts/`, `backend/`, or `zk/` directory), say so rather than inventing output.

## 3. Spec-compliance check

When asked to review/check code against project conventions, grep for and flag these specific violations (cite the doc section):

**Solidity** (`docs/solidity.md`):
- `Ownable` used instead of `AccessControlUpgradeable` on a core contract
- Missing `nonReentrant` on an external function that transfers value or calls out
- Raw `.transfer()`/`.approve()`/`.transferFrom()` on an `IERC20` instead of `SafeERC20`'s `safeTransfer`/`safeTransferFrom`
- State-changing function with no corresponding event, or an event not indexed on fields likely to be filtered
- Unbounded loop over a user-supplied/growable array
- `unchecked {}` block without a comment justifying why it's safe
- Custom fixed-point math instead of a vetted library (PRBMath/Solmate FixedPointMathLib)
- Storage variable reordering/removal in an upgradeable contract (breaks append-only layout)
- Signed data (EIP-712 or otherwise) missing chainId/nonce/contract address

**Go backend** (`docs/go-backend.md`):
- String-concatenated SQL instead of parameterized `$1, $2...` queries
- Missing `ON CONFLICT`/upsert handling on indexer writes (breaks idempotency)
- Hardcoded credentials instead of env-var config
- Redis keys without a TTL, or not namespaced `pb:{module}:{entity}:{id}`
- Blocking function missing `context.Context` as first parameter
- Shared mutable state across goroutines without a mutex/channel

**zk** (`docs/zk.md`):
- Hand-written verifier logic instead of generated (snarkjs/Nargo) output
- Private inputs logged or left in memory without zeroing
- Missing nullifier check before executing a gated action (replay risk)

Report findings with file:line, the specific rule violated, and the doc it comes from. Don't flag stylistic preferences that aren't backed by one of the docs.
