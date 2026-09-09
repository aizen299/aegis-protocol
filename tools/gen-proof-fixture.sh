#!/usr/bin/env bash
# Regenerates contracts/test/utils/ProofFixture.sol: a real proof the contract suite verifies.
#
# The fixture is committed so `forge test` needs neither nargo nor bb, the same reason the Poseidon
# bytecode is committed. If the circuit changes, the verifying key changes and this proof stops
# verifying — that is the drift being caught, not a flake, and `make zk-verifier-check` names it.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CIRCUIT="$ROOT/zk/circuits/vault_membership"
OUT="${1:-$ROOT/contracts/test/utils/ProofFixture.sol}"

for bin in nargo bb node python3; do
  command -v "$bin" >/dev/null || { echo "gen-proof-fixture: $bin is not on PATH" >&2; exit 1; }
done

work="$(mktemp -d)"
trap 'rm -rf "$work"; rm -f "$CIRCUIT/Prover.toml"' EXIT

# The witness is built with circomlibjs rather than by reimplementing Poseidon here. That the
# circuit accepts it is itself a check that the JS and Noir hashes agree.
node "$ROOT/tools/poseidon/witness.cjs" > "$CIRCUIT/Prover.toml"

cd "$CIRCUIT"
nargo compile
nargo execute
bb write_vk --verifier_target evm -b target/vault_membership.json -o "$work/vk"
bb prove --verifier_target evm -b target/vault_membership.json -w target/vault_membership.gz \
  -k "$work/vk/vk" -o "$work/proof"

python3 "$ROOT/tools/render-proof-fixture.py" "$work/proof/proof" "$work/proof/public_inputs" "$OUT"
echo "wrote $OUT"
