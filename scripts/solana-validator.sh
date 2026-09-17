#!/usr/bin/env bash
# Starts solana-test-validator with every Solana program at its declared id, upgradeable, and a fresh
# keypair as their upgrade authority. Only that authority may initialize them, so tests need the key.
#
# Usage: solana-validator.sh DIR
# Writes DIR/authority.json and DIR/validator.pid, and returns once the validator is healthy.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIR="${1:?usage: solana-validator.sh DIR}"
RPC=http://127.0.0.1:8899

mkdir -p "$DIR"
solana-keygen new --no-bip39-passphrase --silent --force --outfile "$DIR/authority.json" >/dev/null
AUTHORITY=$(solana-keygen pubkey "$DIR/authority.json")

# Every program with a committed IDL, each at the id its IDL declares.
PROGRAMS=()
for idl in "$ROOT"/solana/idl/*.json; do
  name=$(basename "$idl" .json)
  so="$ROOT/solana/target/deploy/$name.so"
  # A program with a localnet build uses it here; see its `localnet` feature.
  [ -f "$ROOT/solana/target/localnet/$name.so" ] && so="$ROOT/solana/target/localnet/$name.so"
  [ -f "$so" ] || { echo "solana-validator: $so is missing; run 'make solana-build'" >&2; exit 1; }
  address=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['address'])" "$idl")
  PROGRAMS+=(--upgradeable-program "$address" "$so" "$AUTHORITY")
done

# Wormhole's mainnet programs, at their mainnet addresses. solana/external/README.md.
PROGRAMS+=(--bpf-program worm2ZoG2kUd4vFXhvjh93UUH596ayRfgQ2MgjNMTth "$ROOT/solana/external/wormhole_core_bridge.so")
PROGRAMS+=(--bpf-program EFaNWErqAtVWufdNb7yofSHHfWFos843DFpu4JBw24at "$ROOT/solana/external/wormhole_verify_vaa_shim.so")

# The default keeps 10,000 shreds, and a suite that indexes from slot 1 outlives that: the indexer then
# rightly refuses the pruned range. Local history is kept for the whole run.
nohup solana-test-validator --reset --quiet --ledger "$DIR/ledger" --limit-ledger-size 2000000 "${PROGRAMS[@]}" \
  >"$DIR/validator.log" 2>&1 &
echo $! > "$DIR/validator.pid"

healthy() {
  curl -sf "$RPC" -H 'content-type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"getHealth"}' | grep -q '"ok"'
}
for _ in $(seq 1 90); do
  healthy && exit 0
  sleep 1
done
echo "solana-validator: not healthy after 90s" >&2
tail -20 "$DIR/validator.log" >&2 || true
exit 1
