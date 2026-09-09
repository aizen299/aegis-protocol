# Circuits — v0.4

Noir. The framework decision is recorded in `docs/v0.4-zk-plan.md` §2.1: Noir over Circom, because
Groth16 needs a per-circuit trusted setup that recurs on every circuit change, while UltraPlonk's
universal SRS does not.

| Circuit | Proves |
|---|---|
| `vault_membership` | the prover owns a commitment in the vault's tree, without revealing which |

## Toolchain

Pinned in `toolchain.txt` and enforced by `make zk-toolchain-check`. The generated Solidity verifier
is a function of these versions, so they are pinned as strictly as `solc_version`.

```bash
noirup --version $(awk '/^nargo /{print $2}' toolchain.txt)
make zk-toolchain-check
make zk-circuits-test
```

## Tests

The negative cases are the point. A membership circuit that accepts everything passes every
positive test ever written, so each failure asserts *why* it failed, not only that it did — the
first run of these tests caught a swapped argument pair that a bare `should_fail` would have
passed.

The suite is mutation-checked: removing the root binding, the path-index constraint, or the
nullifier's domain separation each breaks the tests that exist to catch it.

Compiled artifacts and generated verifiers are build output, never committed. The Solidity verifier
is generated into `contracts/src/zk/` by `nargo codegen-verifier` and never hand-written.
