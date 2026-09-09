# Deferred work

Known gaps, each with a trigger that will actually fire. A deferral whose trigger never arrives is
a decision to drop the work, so anything without a real trigger is recorded as accepted risk
instead.

Reviewed at each version boundary.

---

## Accepted for v0.1

### A vault halts while a yield venue is unreachable

`totalAssets()` reads the strategy, and it sits on the path of every deposit and withdrawal. A
venue that reverts — a paused lending market is the ordinary case, not an exotic one — halts the
vault until `DEFAULT_ADMIN_ROLE` calls `detachStrategy()`.

`detachStrategy` exists so the state is recoverable rather than permanent (see
`test/integration/StrategyFailure.t.sol`), but recovery still needs a multisig transaction, which
could take hours. During that window nobody can exit, including holders whose share is entirely
idle.

**The alternative considered:** wrap the strategy read in `try/catch` and fall back to the last
recorded allocation, so the vault degrades instead of halting. Not taken, because a stale figure
means exits during the outage are priced against a number nobody can verify. If the venue really
did lose the money, early exits are paid in full out of the assets belonging to whoever stays —
trading a liveness failure for a silent solvency transfer. Halting is the more honest failure.

**Revisit when** a second strategy slot exists, or a vault is deployed against a venue whose pause
behaviour is outside our control. Whichever comes first.

### Overstated venue holdings inflate the share price

If a strategy reports more than it holds, the vault believes it. Early exits are paid at the
inflated rate out of real assets and the shortfall lands on whoever leaves last, which
`test_overstatedHoldings_payEarlyExitsFromOthersAssets` demonstrates.

There is no defence against this inside the vault: a strategy is trusted code, set by
`VAULT_MANAGER_ROLE`. The mitigation is procedural — strategies are reviewed before they are
attached, and `maxStrategyAllocationBps` caps the exposure.

**Revisit at** v1.0 hardening, alongside the strategy review process. Not a code change on its own.

### Branch coverage on `VaultEngine` is 67%

Line coverage is 93%. The uncovered branches are mostly revert guards on parameter setters, which
carry no value at risk.

**Revisit when** coverage falls, or when a v0.2+ module adds a caller to the vault's admin surface.

---

## Accepted for v0.3

### The timelock's admin can route around the governor

`Timelock` grants `TIMELOCK_PROPOSER_ROLE`, `TIMELOCK_EXECUTOR_ROLE`, and
`TIMELOCK_CANCELLER_ROLE` to the `Governor` alone, and `test_nobodyButTheGovernorCanDriveTheTimelock`
holds that. Nothing structural stops `DEFAULT_ADMIN_ROLE` from granting the executor role to another
account, which could then run a scheduled operation directly — the action would take effect while
the proposal sat in the indexer still marked `queued`.

This is the same trust already placed in that key, and it is the mechanism that lets a bricked
governor be replaced without migrating every protocol role, which is the reason the contracts were
split at all. Removing it would mean giving up the recovery path.

**Revisit when** the §2.4 role migration runs and `DEFAULT_ADMIN_ROLE` moves to the timelock itself,
at which point routing around the governor requires a passed proposal and the divergence closes.

---

## Deferred to a named version

### Indexer lag metric and its CloudWatch alarm

`docs/devops.md` lists "indexer lag > 100 blocks" as a required v1.0 alarm. The indexer logs its
lag but emits no metric, so `infra/modules/ecs/alarms.tf` alarms on the task disappearing instead —
which catches a crashed indexer but not a silently stalled one.

**Trigger:** v1.0, with the rest of the observability work.

---

## Not deferred — decided

| Question | Decision |
|---|---|
| Strategy failure integration tests | Done in v0.1. `test/integration/StrategyFailure.t.sol` — they found the halt-and-jam defect that `detachStrategy` now fixes. |
| Token decimals | Never assumed. Resolved per asset, stored in `assets`, enforced by a foreign key. |
| Event identity | `(chain_id, tx_hash, log_index)`. `(chain_id, tx_hash)` silently drops events. |
| Share scale in API responses | Served, resolved not assumed. The indexer reads `virtualSharesOffset()` and records it in `vaults`; the position query joins it. It was withheld for one release rather than guessed. |
| API handler tests | Done in v0.2. `internal/api` covers validation, error mapping, pagination bounds, and scale serialisation against stubs; the end-to-end suite covers the same handlers over real indexed rows. |
| Vault metadata table | Done in v0.2. `vaults` completes the pattern `assets` and `oracle_feeds` follow: a foreign key from every share-bearing row, so a share cannot be stored without its scale. |
