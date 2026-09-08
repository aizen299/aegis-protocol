# Contracts

Solidity + Foundry. Settlement layer only — protocol intelligence lives in `backend/`.

## Status

| Version | Module | State |
|---|---|---|
| v0.1 | `src/vault/` | Implemented |
| v0.2 | `src/oracle/` | Not started |
| v0.3 | `src/governance/` | Not started |
| v0.4 | `src/zk/` | Not started |

## Commands

```bash
forge build --sizes
forge test -vvv
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

The `reentrancy-balance` findings previously raised on `withdraw` were resolved structurally, not
suppressed: liquidity sourcing moved into `_ensureLiquidity`, which reads the balance again after
the strategy call and compares only the post-call value.

## Upgrade procedure

Storage layout is append-only. Before any upgrade:

```bash
forge inspect VaultEngine storage-layout > new-layout.json
# diff against the layout committed for the deployed release
```

Never remove or reorder a variable. Consume `__gap` when appending.
