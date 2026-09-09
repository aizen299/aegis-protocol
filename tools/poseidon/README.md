# Poseidon generator

Generates `contracts/src/zk/poseidon/PoseidonBytecode.sol` from circomlib.

Poseidon is a cryptographic primitive, so it is generated and never hand-written — the same rule
`docs/zk.md` applies to the zk verifier, and it matters more here: a single wrong round constant
produces a Merkle tree that no proof can ever verify against, and the failure appears only at
integration.

## Why the output is committed

The circuit and the contract must compute the same hash. Making that agreement depend on an npm
install at build time would put a network fetch on the path of every `forge test`. Instead the
artifact is committed, and CI regenerates it and fails on any difference — the same pattern the
ABI-drift job uses for `backend/pkg/contracts`.

**Node is required only to regenerate.** `forge build` and `forge test` read the committed Solidity
and never invoke this tool.

## Commands

```bash
make poseidon-gen      # regenerate and write into contracts/src/zk/poseidon
make poseidon-check    # regenerate into a temp dir and fail on any difference
```

## Reproducibility

`circomlibjs` is pinned to an exact version in `package.json`, and `package-lock.json` is committed
so the whole dependency tree is fixed. `make poseidon-check` uses `npm ci`, which installs from the
lockfile and fails rather than resolving anything new. `poseidonContract.createCode` is
deterministic for a fixed version — verified by generating twice and comparing.

Bumping the version is a deliberate act: change `package.json`, run `make poseidon-gen`, and commit
the regenerated artifact. `contracts/test/unit/Poseidon.t.sol` then proves the new bytecode still
agrees with the circuit's vectors, and the layout of the tree it builds is unchanged.

## Cross-check

The agreement between the circuit and the contract is a claim, so it is tested on both sides
against the same constants:

| | Asserted in |
|---|---|
| `Poseidon([1])` = `0x29176100…0133` | `zk/circuits/vault_membership/src/main.nr`, `contracts/test/unit/Poseidon.t.sol` |
| `Poseidon([1,2])` = `0x115cc0f5…189a` | both of the above |

If either side's hash family changes, one of the two suites fails.
