#!/usr/bin/env python3
"""Write, or check, the circuit artifact the browser proves with.

The compiled circuit is gitignored, so the frontend serves a committed copy — and a committed copy of
a generated file drifts unless something checks it. This compares it against a fresh `nargo compile`.

Only the fields a proof depends on are kept: abi and bytecode, plus the compiler version and hash for
diagnosis. `file_map` and `debug_symbols` are dropped. They hold absolute paths from whichever machine
compiled, so they would publish a local username in a public web asset, and they differ between a
laptop and a CI runner, so a check that compared them could never pass.

Usage: circuit-artifact.py write | check
"""

import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
COMPILED = ROOT / "zk/circuits/vault_membership/target/vault_membership.json"
SERVED = ROOT / "frontend/public/circuits/vault_membership.json"
KEPT = ("noir_version", "hash", "abi", "bytecode")
COMPARED = ("abi", "bytecode")


def stripped(path: Path) -> dict:
    data = json.loads(path.read_text())
    missing = [k for k in KEPT if k not in data]
    if missing:
        sys.exit(f"circuit-artifact: {path} lacks {missing}; an unreadable artifact must not match")
    if not data["bytecode"]:
        sys.exit(f"circuit-artifact: {path} has empty bytecode")
    return {k: data[k] for k in KEPT}


def main() -> int:
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    if not COMPILED.exists():
        sys.exit("circuit-artifact: no compiled circuit; run nargo compile first")

    fresh = stripped(COMPILED)

    if mode == "write":
        SERVED.parent.mkdir(parents=True, exist_ok=True)
        SERVED.write_text(json.dumps(fresh, separators=(",", ":")) + "\n")
        print(f"wrote {SERVED.relative_to(ROOT)} ({SERVED.stat().st_size} bytes)")
        return 0

    if mode != "check":
        sys.exit("usage: circuit-artifact.py write | check")

    if not SERVED.exists():
        print("FAIL  frontend/public/circuits/vault_membership.json is missing")
        return 1

    served = stripped(SERVED)
    drifted = [k for k in COMPARED if served[k] != fresh[k]]
    if drifted:
        print(f"FAIL  the browser's circuit artifact differs from a fresh compile in: {', '.join(drifted)}")
        print("      Proofs made in the browser would be for a different circuit than the verifier's.")
        print("      Run: python3 scripts/circuit-artifact.py write")
        return 1

    for leak in ("file_map", "debug_symbols"):
        if leak in json.loads(SERVED.read_text()):
            print(f"FAIL  the served artifact carries {leak}, which holds local filesystem paths")
            return 1

    print("ok    the browser's circuit artifact matches a fresh compile")
    return 0


if __name__ == "__main__":
    sys.exit(main())
