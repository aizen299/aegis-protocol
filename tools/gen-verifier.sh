#!/usr/bin/env bash
# Generates the zk verifier artifacts from the pinned Noir toolchain.
#
# The verifier is generated and never hand-written — docs/zk.md is explicit, and a hand-edited
# verifier is a soundness hole that no test would catch.
#
# Output: contracts/generated/HonkVerifier.sol
#
# It lives outside contracts/src on purpose. Slither 0.11.5 crashes parsing it — it cannot
# constant-fold an array size the generator emits — and Slither over hand-written code is mandatory
# here. `slither src/` therefore never sees it, while Foundry still compiles and links it because
# the tests import it. The exclusion is structural rather than a config flag that could quietly
# stop applying.
#
# Committing bytecode instead, the way Poseidon is handled, is not an option: this verifier links
# two external libraries, so its creation code carries unlinked placeholders.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CIRCUIT="$ROOT/zk/circuits/vault_membership"
OUT_SOL="${1:-$ROOT/contracts/generated}"

for bin in nargo bb forge jq; do
  command -v "$bin" >/dev/null || { echo "gen-verifier: $bin is not on PATH" >&2; exit 1; }
done

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

cd "$CIRCUIT"
nargo compile
bb write_vk --verifier_target evm -b target/vault_membership.json -o "$work/vk"
bb write_solidity_verifier -k "$work/vk/vk" -o "$work/HonkVerifier.sol"

mkdir -p "$OUT_SOL"
cp "$work/HonkVerifier.sol" "$OUT_SOL/HonkVerifier.sol"

if [ ! -s "$OUT_SOL/HonkVerifier.sol" ]; then
  echo "gen-verifier: generated an empty verifier — an unreadable artifact must not read as valid" >&2
  exit 1
fi

echo "wrote $OUT_SOL/HonkVerifier.sol"
