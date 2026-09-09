#!/usr/bin/env bash
# Generates a vault-membership proof for a specific gate, chain and action.
#
# Unlike the committed fixture, everything here is bound to values known only after deployment,
# which is the point: the gate's address and the chain id are public inputs, and a pipeline that
# only ever proves against a hardcoded address has not been tested against the coupling that
# actually bites.
#
# Usage: prove.sh <gate-address> <chain-id> <action-id> <secret> <out-dir>
# Writes <out-dir>/proof.hex and <out-dir>/public_inputs.hex.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CIRCUIT="$ROOT/zk/circuits/vault_membership"

GATE="${1:?gate address required}"
CHAIN_ID="${2:?chain id required}"
ACTION_ID="${3:?action id required}"
SECRET="${4:?secret required}"
OUT="${5:?output directory required}"

for bin in nargo bb node; do
  command -v "$bin" >/dev/null || { echo "prove: $bin is not on PATH" >&2; exit 1; }
done

work="$(mktemp -d)"
trap 'rm -rf "$work"; rm -f "$CIRCUIT/Prover.toml"' EXIT

GATE="$GATE" CHAIN_ID="$CHAIN_ID" ACTION_ID="$ACTION_ID" SECRET="$SECRET" \
  node "$ROOT/tools/poseidon/witness.cjs" > "$CIRCUIT/Prover.toml"

cd "$CIRCUIT"
nargo compile >/dev/null
nargo execute >/dev/null
bb write_vk --verifier_target evm -b target/vault_membership.json -o "$work/vk" >/dev/null 2>&1
bb prove --verifier_target evm -b target/vault_membership.json -w target/vault_membership.gz \
  -k "$work/vk/vk" -o "$work/proof" >/dev/null 2>&1

mkdir -p "$OUT"
python3 - "$work/proof/proof" "$work/proof/public_inputs" "$OUT" <<'PY'
import pathlib, sys
proof = pathlib.Path(sys.argv[1]).read_bytes()
inputs = pathlib.Path(sys.argv[2]).read_bytes()
out = pathlib.Path(sys.argv[3])
if not proof or len(inputs) != 5 * 32:
    sys.exit(f"prove: malformed output (proof {len(proof)} bytes, inputs {len(inputs)} bytes)")
(out / "proof.hex").write_text("0x" + proof.hex())
(out / "public_inputs.hex").write_text("\n".join(
    "0x" + inputs[i : i + 32].hex() for i in range(0, len(inputs), 32)
))
PY
echo "wrote $OUT/proof.hex and $OUT/public_inputs.hex"
