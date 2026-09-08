# Oracle Module — v0.2 (in progress)

Plan and open decisions: [`docs/v0.2-oracle-plan.md`](../../../docs/v0.2-oracle-plan.md).

| Component | State |
|---|---|
| `OracleStaking.sol` | Implemented — registration, stake, unbonding, capped slashing |
| Round lifecycle and submissions | Not started |
| On-chain median and settlement | Not started |
| `IOracleReader` consumer interface | Not started |

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
