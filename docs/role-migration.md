# Role migration runbook

How protocol roles move from the admin multisig to governance, and in what order.

This is the procedure `docs/v0.3-governance-plan.md` §2.4 promised. It is executed by
`contracts/test/integration/RoleMigration.t.sol` and by
`backend/internal/e2e/role_migration_test.go` against a local chain, so every claim here has been
run rather than only written down.

**Nothing in v0.3 performs this migration.** v0.3 builds the capability; exercising it is a v1.0
step, after the internal audit simulation the roadmap calls for. Granting an unaudited governance
system authority over live contracts inverts the safe ordering.

---

## The recipient is the timelock, not the governor

Roles are granted to `Timelock`. It is the `msg.sender` of every executed action, so it is the
account a governed contract sees; granting the `Governor` instead looks identical until the first
execution fails.

It also means a governor can later be replaced by re-granting three roles on one contract rather
than migrating every protocol role again, and that a bug in vote counting does not immediately mean
loss of the protocol — the delay and the canceller still stand between a vote and a call.

---

## Order

Most reversible first. Each stage is only entered once the previous one has been observed working.

### Stage 1 — grant, while the multisig retains

```
multisig: grantRole(ROLE, timelock)
```

Both hold the role. Nothing has been given up, and this is the only fully reversible stage.

**Verify:** the timelock holds it, the multisig still holds it, and the governor holds nothing.

### Stage 2 — exercise it through a proposal

Put one real, low-consequence change through the whole machine: propose, vote, queue, wait out the
delay, execute. A parameter with an observable value is the right first target.

**Verify:** the value changed, and it did *not* change before the delay elapsed.

**Rollback:** while the multisig still holds the role it can `revokeRole(ROLE, timelock)`. This is
the reason stage 1 keeps the multisig, and it is the last point at which withdrawal is free.

### Stage 3 — the multisig renounces, one role at a time

```
multisig: renounceRole(ROLE, multisig)
```

One role per step, verifying between each. Reversible while `DEFAULT_ADMIN_ROLE` remains with the
multisig, which can grant it back.

**Verify:** the multisig can no longer perform the action, and governance still can.

### Stage 4 — `DEFAULT_ADMIN_ROLE`, last and irreversible

```
multisig: grantRole(DEFAULT_ADMIN_ROLE, timelock)
multisig: renounceRole(DEFAULT_ADMIN_ROLE, multisig)
```

After this, only a passed proposal can grant or revoke any role on that contract. There is no
recovery path if governance is broken, so it is the final step and only after every other role has
been exercised by governance in production.

---

## What never moves

`SLASHER_ROLE` is deliberately excluded and may never migrate. Its value is being rotatable in
minutes: it is a hot key held by a backend service, and the response to a suspected compromise is
to rotate it immediately. Routing that rotation through a multi-day timelock is strictly worse for
the exact threat the role exists to answer.

If slashing policy should become governable, the governable thing is the *parameters* — the cap, the
reasons, the appeal window — never the key itself.

---

## Suggested sequence

| Order | Role | Why here |
|---|---|---|
| 1 | `VAULT_MANAGER_ROLE` | Parameters only, observable, no custody |
| 2 | `ORACLE_MANAGER_ROLE` | Feed registration and round parameters |
| 3 | `PAUSER_ROLE` | Pausing through a multi-day delay is close to useless; keep an emergency pauser alongside |
| 4 | `UPGRADER_ROLE` | Code, so only after governance has a track record |
| 5 | `DEFAULT_ADMIN_ROLE` | Irreversible |
| — | `SLASHER_ROLE` | Never |

`PAUSER_ROLE` deserves a note: a pause that takes two days to enact does not answer the incident it
exists for. If it moves to governance at all, an emergency pauser must be retained beside it, and
that account is then a live admin key with everything that implies.

---

## Verifying a migration

```bash
cast call <contract> "hasRole(bytes32,address)(bool)" $(cast keccak "VAULT_MANAGER_ROLE") <timelock>
cast call <contract> "hasRole(bytes32,address)(bool)" $(cast keccak "VAULT_MANAGER_ROLE") <multisig>
cast call <contract> "hasRole(bytes32,address)(bool)" $(cast keccak "SLASHER_ROLE") <timelock>
```

Check the negative space too. A migration that granted more than intended is invisible in a
per-role check of the roles you meant to grant.
