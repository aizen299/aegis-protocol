#!/usr/bin/env bash
# Generates a proof through the browser proving path and verifies it against the committed verifier.
#
# The Rust service shells out to the nargo and bb binaries; the browser uses noir_js and bb.js. Two
# implementations, one verifier, and nothing making them agree except this. See
# docs/v1.3-write-actions-plan.md §2.2.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CIRCUIT="$ROOT/zk/circuits/vault_membership"
ARTIFACTS="$ROOT/contracts/test/artifacts"

# The gate and submitter the committed fixture was proved for, so this exercises the same domain.
GATE="${GATE:-0xa93bcdc55c1d7eb0801caaabe9525fa1857f3c35}"
SUBMITTER="${SUBMITTER:-0x1111111111111111111111111111111111111111}"

for bin in nargo node python3; do
  command -v "$bin" >/dev/null || { echo "browser-proof-check: $bin is not on PATH" >&2; exit 1; }
done

python3 "$ROOT/scripts/check-proving-versions.py"

work="$(mktemp -d)"
trap 'rm -rf "$work"; rm -f "$CIRCUIT/Prover.toml"' EXIT

echo "==> compiling the circuit"
(cd "$CIRCUIT" && nargo compile)

echo "==> building a witness"
GATE="$GATE" SUBMITTER="$SUBMITTER" node "$ROOT/tools/poseidon/witness.cjs" > "$work/prover.toml"
python3 - "$work/prover.toml" "$work/prover.json" <<'PY'
import json, re, sys
out = {}
for line in open(sys.argv[1]):
    if not line.strip():
        continue
    key, value = line.split("=", 1)
    key, value = key.strip(), value.strip()
    out[key] = re.findall(r'"([^"]+)"', value) if value.startswith("[") else value.strip('"')
if len(out) < 9:
    sys.exit(f"browser-proof-check: witness has {len(out)} fields, expected 9")
json.dump(out, open(sys.argv[2], "w"))
PY

echo "==> proving through noir_js and bb.js, WASM backend"
mkdir -p "$ARTIFACTS"
(cd "$ROOT/frontend" && node tools/prove-browser.mjs \
  "$CIRCUIT/target/vault_membership.json" "$work/prover.json" "$work/out")

# An empty artifact must fail as empty rather than be verified as nothing.
[ -s "$work/out/proof.hex" ] || { echo "browser-proof-check: the proof is empty" >&2; exit 1; }
cp "$work/out/proof.hex" "$ARTIFACTS/browser-proof.hex"
cp "$work/out/public_inputs.hex" "$ARTIFACTS/browser-public-inputs.hex"

echo "==> verifying against the committed verifier"
(cd "$ROOT/contracts" && forge test --match-contract BrowserProofTest -vv)
