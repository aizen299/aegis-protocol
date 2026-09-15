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

## Accepted for v0.4

### A secret touches disk while a proof is generated

`nargo` reads its witness from a `Prover.toml` on disk, so the proof service writes one containing
the caller's secret. It is created with mode 0600 and removed when proving returns, on the failure
path as much as the success one, and `no_witness_file_survives_a_run` asserts that. But between
those two moments the material is on a filesystem, where a crash, a core dump, or a backup could
capture it.

Removing this means feeding the witness to the prover over a pipe or in memory, which the `nargo`
CLI does not offer. The alternative is linking Barretenberg directly — a second implementation of
the proving path, and a second thing that can disagree with the binaries that generate the verifier
the contract deploys.

**Revisit when** the toolchain gains an in-memory witness interface, or when the service moves to a
Barretenberg binding for other reasons. Either makes this disappear rather than mitigating it.

---

## Accepted for v1.0

Found by the internal audit simulation, `docs/v1.0-audit-simulation.md`. Severities and full failure
scenarios are there; this records the triggers.

The three frontend entries are the exception: they were found after the tag, by running the UI
against a real local stack rather than by the review — which had omitted `frontend/` from its scope.

### A zk proof can be submitted by anyone who sees it — High

Nothing binds a proof to a submitter. An observer can copy a proof out of the mempool and land it
first, permanently burning the honest user's nullifier — their deposit is safe, their capability is
destroyed. `test_aStrangerCanSubmitSomeoneElsesProof` is left passing as the record, and will fail
the moment a binding is added.

Accepted only because the gate confers no benefit today: it marks a nullifier spent and emits an
event nothing consumes. The fix is a sixth public input for the permitted submitter, a regenerated
verifier, and a regenerated fixture — v0.4 work, and shipping a freshly changed circuit under a
hardening release would trade a known risk for an untested one.

**Trigger — not a date.** Before any contract consumes `PrivateActionExecuted` or gates on
`isSpent`. At that point a frontrunner receives what the gate confers and this becomes theft.

### No penalty for a node that never submits (ORC-1) — Medium

`docs/oracle.md` lists four slashable conditions. Two are implemented: `OUTLIER_SUBMISSION` and
`INVALID_SIGNATURE`. `MISSED_ROUND` and `CONSECUTIVE_MISSES` are not, and the aggregator iterates
the submissions a round received rather than the eligible nodes that did not submit, so a silent
node is invisible to it.

The dead `ReasonMissedRound` and `penaltyMissedRoundBps` constants have been **removed**. They made
the gap read as implemented, and unused Go constants do not fail a build.

Quorum is judged against `eligibleCount`, frozen at `openRound` from `activeNodeCount`. Nodes that
stake and never submit raise the denominator without raising the numerator: an adversary holding
just over 40% of active slots at a 60% quorum halts the feed, is never slashed, and withdraws in
full after unbonding. The cost is the time-value of locked capital and nothing else — which is
exactly the cost the missing penalty exists to raise.

Accepted because implementing it reopens v0.2: the aggregator would need the eligible set per round,
not just the submissions, and the penalty is only meaningful once nodes are operated by parties who
do not already bear the cost of a halted feed.

**Trigger:** the first oracle node operated by someone outside the project, or any contract reading
a feed value to make a decision that moves funds. Either makes a free liveness attack worth
mounting.

### No slippage bound on vault deposit or withdraw (VLT-1) — Medium

`deposit(assets, receiver)` and `withdraw(shares, receiver)` accept no minimum. `docs/solidity.md`'s
vulnerability table lists "Frontrunning — use commit-reveal or slippage params where needed", and
this is the unmet half of it.

Share price moves with `totalAssets()`, which reads the strategy. A withdrawal submitted before a
strategy loss executes at the worse rate with no revert, because the user had no way to express a
floor. The virtual-shares offset defeats the first-depositor inflation attack, which is a different
problem; it does nothing about price movement between submission and inclusion.

Accepted because adding a minimum changes external signatures that the indexer ABI, the frontend,
and the committed storage-layout baselines all track — an interface decision, not a hardening patch
— and because no strategy is deployed, so today the price cannot move between submission and
inclusion at all.

**Trigger:** attaching any strategy on a chain where someone other than the operator holds shares.
At that point the price moves for reasons outside a depositor's control and the bound is the only
thing that lets them say no.

### The UI covers the vault only — Medium

`frontend/` implements v0.1's deposit, withdraw, and dashboard. There is no interface for oracle
feeds, governance proposals and voting, or zk proofs, though the backend serves and end-to-end tests
all three. `frontend/README.md` has tracked this accurately per version throughout; what was missing
was any record of it in this register or in the release documents, which is now corrected.

Not a spec violation — `docs/project-spec.md` §3.5 fixes the frontend stack and never assigns
per-version frontend deliverables. It is still the largest gap between what the protocol does and
what anyone can see it do.

**Trigger:** none needed — this is roadmap step 3, targeted at `v1.1.0`. One ordering constraint is
binding: the zk interface must not ship before the bearer-proof finding above is fixed. A UI is what
first puts real proofs into a mempool, so shipping it against the current gate does not inherit that
risk, it activates it.

### The root history window is fixed at deployment — Low

`CommitmentTree` has no setter for `rootHistorySize`, so the value chosen at initialization is
permanent. At the deployed 30 it is correct; it is simply unchangeable if it turns out not to be.

**Trigger:** the next `CommitmentTree` upgrade, which is the only occasion a setter can be added, or
evidence of honest proofs failing on the root window.

### `setMaxNodes` has no ceiling over an O(n²) settlement sort — Low

`_median` is an insertion sort bounded by `maxNodes`. The bound is enforced, but `setMaxNodes`
accepts any non-zero value, so `ORACLE_MANAGER_ROLE` can raise it until settlement no longer fits in
a block. Trusted role, loud failure.

**Trigger:** raising `maxNodes` beyond the low hundreds, or a deployment where
`ORACLE_MANAGER_ROLE` is not the timelock.

### The oracle submission nonce is per node, not per feed — Low

A node serving two feeds must have its transactions mined in the order it signed them; reordering
reverts the later one and forces a re-sign. Replay protection is unaffected — this is operability.

**Trigger:** a second production feed, or the first `InvalidNonce` revert from an honest node.

---

## Deferred to a named version

### The AWS staging deployment

`docs/project-spec.md` §5 lists an AWS staging deployment for v1.0 and §7 names Arbitrum Sepolia.
Neither happens: no AWS account exists for this project and none is planned. v1.0 therefore ships
as production-*ready* — infrastructure defined, validated, and preflight-checked — and has never
been applied.

What stays unproven, stated rather than implied:

- **That the Terraform applies at all.** `terraform validate` checks syntax and types. Quotas, IAM
  propagation delays, subnet and AZ mismatches, and parameter-group defaults are all unexercised.
- **That an ECS task role can reach SSM.** §2.6's secret path is proven against LocalStack, which
  exercises the SDK and the protocol but not the authorization.
- **A staging lag baseline.** The indexer-lag alarm threshold stays provisional at 300s, stated at
  the resource.
- **The role migration on staging.** Re-scoped to a local Anvil deployment, which is where the
  runbook was exercised at v0.3 step 8. Stage 4 was always out of scope.
- **Whether `aggregator` and `oraclenode` get ECS task definitions.** The scope question raised in
  step 4 of the v1.0 plan is deferred unanswered, because it cannot be settled without an account
  to deploy into.

**Trigger:** an AWS account existing. The first thing worth doing with one is a single `terraform
apply` followed immediately by `terraform destroy` — it costs cents and converts "validated" into
"applied at least once", which is the whole of the gap above that money actually buys.

---

## Not deferred — decided

| Question | Decision |
|---|---|
| Strategy failure integration tests | Done in v0.1. `test/integration/StrategyFailure.t.sol` — they found the halt-and-jam defect that `detachStrategy` now fixes. |
| Token decimals | Never assumed. Resolved per asset, stored in `assets`, enforced by a foreign key. |
| Event identity | `(chain_id, tx_hash, log_index)`. `(chain_id, tx_hash)` silently drops events. |
| Share scale in API responses | Served, resolved not assumed. The indexer reads `virtualSharesOffset()` and records it in `vaults`; the position query joins it. It was withheld for one release rather than guessed. |
| API handler tests | Done in v0.2. `internal/api` covers validation, error mapping, pagination bounds, and scale serialisation against stubs; the end-to-end suite covers the same handlers over real indexed rows. |
| The dashboard could not distinguish a failed read from an empty vault | Fixed after v1.0. Reads now resolve to loading, failed, or value; a failed vault read shows an alert saying the figures are unavailable rather than zero. Mutation-tested by restoring the original collapsed rendering, which fails four tests. |
| The frontend has no tests | Fixed after v1.0. Vitest and React Testing Library, 18 tests over the dashboard's read states and the decimals handling, wired into Frontend CI before the build — a build proves compilation, not behaviour. Coverage is the vault surface only; new surfaces need their own. |
| Indexer lag metric and its alarm | Done in v1.0. Emitted in seconds and blocks through CloudWatch EMF, with `indexer_lag_seconds` alarming on a stalled indexer rather than only a crashed one. Was deferred from v0.2. |
| Vault metadata table | Done in v0.2. `vaults` completes the pattern `assets` and `oracle_feeds` follow: a foreign key from every share-bearing row, so a share cannot be stored without its scale. |
