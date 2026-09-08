# Oracle Module — v0.2 (in progress)

Plan and open decisions: [`docs/v0.2-oracle-plan.md`](../../../docs/v0.2-oracle-plan.md).

| Component | State |
|---|---|
| `OracleStaking.sol` | Implemented — registration, stake, unbonding, capped slashing |
| `OracleRounds.sol` | Implemented — lifecycle, EIP-712 submissions, on-chain median |
| `IOracleReader` | Implemented — fail-closed staleness |
| Aggregation service, node binary | Not started |

## Why two contracts

`docs/oracle.md` describes one `OracleModule.sol`. Combined they compile to 25,859 bytes, past the
24,576-byte limit — one contract was never going to fit. Beyond size, `OracleStaking` custodies node
balances and should be upgraded rarely, while round logic is where iteration happens; separate
proxies keep those risk profiles apart. The coupling is read-only: `OracleRounds` calls
`nodeSetVersion()` and `isEligibleAt()`, so neither holds a role on the other.

## Why quorum uses a snapshot

Quorum is 2/3 of the node set *as it was when the round opened*. Read live at settlement, the
denominator would be under the nodes' own control — deactivate mid-round and the bar you must clear
drops with it. `OracleStaking` keeps a monotonic `nodeSetVersion`; a round records it and judges
every submission against it. `test_deactivatingMidRoundDoesNotShrinkQuorum` covers nodes leaving,
`test_lateJoinerCannotSubmit` covers nodes arriving.

## Why the reader has no `latestAnswer()`

`getValue(feedId, maxStaleness)` reverts on a value older than the caller's bound, and there is no
default. A getter that returns a price without forcing the caller to state a freshness requirement
is how oracle consumers get exploited: the value looks fine and is hours old.

## Why unbonding exists

Slashing is decided off-chain after a round settles, so stake has to stay locked longer than that
decision takes. Without a delay a node could submit bad data, watch the round settle, and withdraw
before the backend's slash landed — making every penalty in the schedule optional for anyone paying
attention. `docs/oracle.md` does not mention unbonding; this is a deliberate addition, recorded in
the plan at §2.3. `test_unstakeCannotOutrunASlash` pins it.

Requesting an exit deactivates the node immediately, and a slash during unbonding takes precedence
over the request — only what survives it is released.

## Why slashing is capped on-chain

`SLASHER_ROLE` is a backend hot key. The contract caps each slash at 10% of remaining stake so a
compromised key decays a node rather than zeroing it. Every penalty in `docs/oracle.md`'s schedule
(0.5%–10%) fits under the cap, so it constrains an attacker without constraining intended
behaviour.
