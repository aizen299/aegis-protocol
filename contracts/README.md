# Contracts

Solidity + Foundry. Settlement layer only — protocol intelligence lives in `backend/`.

## Status

| Version | Module | State |
|---|---|---|
| v0.1 | `src/vault/` | Implemented — 73 tests, Slither clean |
| v0.2 | `src/oracle/` | Implemented — 150 tests, Slither clean |
| v0.3 | `src/governance/` | Implemented — fuzzed, invariants enforced, layouts baselined at v0.3.0; 261 tests, Slither clean |
| v0.4 | `src/zk/` | In progress — fuzzed and invariant-tested, layouts baselined at v0.4.0; 321 tests, Slither clean |

## Commands

```bash
forge build --sizes
forge test -vvv
forge test --match-path 'test/integration/*' -vvv
forge coverage --report lcov
forge inspect VaultEngine storage-layout   # required before any UUPS upgrade
slither . --config-file slither.config.json
```

## v0.1 VaultEngine

Single-asset vault, UUPS proxy, internal (non-transferable) share accounting, one optional yield
strategy, and manager-configurable risk controls.

**Share math.** Conversions carry a virtual offset of `1e3` shares against `1` asset. This makes the
first-depositor inflation attack cost more than it can extract; `testFuzz_inflationAttack_victimKeepsValue`
asserts a victim retains ≥99.99% of their deposit across a fuzzed donation range.

**Fee-on-transfer.** Deposits credit shares against the measured balance delta, not the requested
amount, so a token that skims on transfer cannot over-mint.

**Pause asymmetry.** `pause()` (PAUSER_ROLE) halts deposits and allocation; withdrawals stay open.
Freezing withdrawals is a separate switch gated on DEFAULT_ADMIN_ROLE, because it traps user funds
and deserves a higher bar than halting inflows.

**Allocation cap.** `maxStrategyAllocationBps` binds at allocation time only. Withdrawals drain the
idle buffer and can leave the realised ratio above the cap; the vault does not force a deallocation
on withdrawal, because an illiquid venue would then block user exits.

**Loss socialisation.** A strategy that loses value reduces `totalAssets()`, so every holder's claim
falls proportionally. Withdrawals pay what the shares are worth, not what was deposited.

## Static analysis

`slither . --config-file slither.config.json` → 0 findings.

Three detector families were suppressed inline after review:

| Detector | Location | Justification |
|---|---|---|
| `incorrect-equality` | `deposit`, `withdraw` zero-guards | `shares == 0` / `assets == 0` are dust-rounding guards, not balance comparisons. The detector targets equality against balances or timestamps. |
| `unused-state` | `__gap` | A reserved storage gap is unreferenced by definition. |
| `low-level-calls` | `Timelock.execute` | An arbitrary call is the mechanism governance exists to provide. A typed interface would restrict governance to functions fixed at deployment. Reachable only through a proposer, an elapsed delay, no cancellation, and an executor; state is set to executed before the call, and the call is `nonReentrant`. |
| `calls-loop` | `CommitmentTree._hash` | Poseidon is its own generated contract, so hashing a Merkle path is necessarily a loop of external calls. The callee is fixed at initialization, pure, and holds no state to corrupt; the loop is bounded by `TREE_DEPTH`. The detector targets an untrusted call that can fail or reenter part-way through a loop. |
| `reentrancy-events` | `Governor.queue` | `ProposalQueued` carries `executableAt`, which the call returns, so it cannot precede the call. The callee is the timelock address fixed at initialization and its `schedule` makes no external call. |
| `timestamp` | `OracleRounds.submit`, `settleRound`, `getValue` | Round deadlines are timestamps by decision (`docs/v0.2-oracle-plan.md` §2.1). The residual is real but bounded: a sequencer can shift a submission across a 300s boundary by seconds, which could exclude one submission near quorum. Quorum is 2/3, so a single excluded submission rarely decides one, and a round that falls short fails closed — no price — rather than settling on a wrong one. |
| `timestamp` | `OracleStaking.completeUnstake` | The comparison guards a 7-day unbonding deadline. Sequencer timestamp drift is seconds-scale, so it cannot move a deadline measured in days; a block count would be the unstable unit on Arbitrum. See `docs/v0.2-oracle-plan.md` §2.1. |

The `reentrancy-balance` findings previously raised on `withdraw` were resolved structurally, not
suppressed: liquidity sourcing moved into `_ensureLiquidity`, which reads the balance again after
the strategy call and compares only the post-call value.

**Venue failure.** `totalAssets()` reads the strategy and sits on the path of every deposit and
withdrawal, so a venue that reverts halts the vault — and `setStrategy` and `emergencyDeallocateAll`
both call the venue too, so a sufficiently broken one blocks its own removal. `detachStrategy()`
(DEFAULT_ADMIN_ROLE) clears the slot without calling out, writing off whatever the venue holds. It
is the only strategy operation that touches nothing external, which is exactly why it works when
nothing else does. `test/integration/StrategyFailure.t.sol` pins this behaviour; the residual
halt-until-multisig window is recorded in [DEFERRED.md](../DEFERRED.md).

## Upgrade procedure

Storage layout is append-only, and the released baseline is committed at
`deployments/layouts/VaultEngine.<release>.json`.

```bash
make contracts-layout-check                    # fails if any layout diverged from its baseline
make contracts-layout-record RELEASE=v0.3.0    # after a deliberate append
```

Every upgradeable contract is listed in the Makefile's `LAYOUT_CONTRACTS`; one absent from that
list is not checked, so adding it there is part of releasing it. An empty layout is treated as a
failure rather than a match — `forge inspect` returns nothing on a stale cache, and comparing
nothing to nothing would otherwise report success for a contract whose layout was never read.

The check runs in CI on every contracts change, so a reordered or removed variable fails the build
rather than surfacing at deploy time. Consume `__gap` when appending.
