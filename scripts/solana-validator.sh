#!/usr/bin/env bash
# Starts solana-test-validator with the vault program at its declared id, upgradeable, and a fresh
# keypair as its upgrade authority. Only that authority may create a vault, so tests need the key.
#
# Usage: solana-validator.sh DIR
# Writes DIR/authority.json and DIR/validator.pid, and returns once the validator is healthy.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIR="${1:?usage: solana-validator.sh DIR}"
RPC=http://127.0.0.1:8899

VAULT_SO="$ROOT/solana/target/deploy/aegis_vault.so"
[ -f "$VAULT_SO" ] || { echo "solana-validator: $VAULT_SO is missing; run 'make solana-build'" >&2; exit 1; }
VAULT_PROGRAM=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['address'])" "$ROOT/solana/idl/aegis_vault.json")

mkdir -p "$DIR"
solana-keygen new --no-bip39-passphrase --silent --force --outfile "$DIR/authority.json" >/dev/null
nohup solana-test-validator --reset --quiet --ledger "$DIR/ledger" \
  --upgradeable-program "$VAULT_PROGRAM" "$VAULT_SO" "$(solana-keygen pubkey "$DIR/authority.json")" \
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
