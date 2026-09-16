#!/usr/bin/env python3
"""Fail if the browser's proving packages differ from the pinned CLI toolchain.

Two proving implementations must produce proofs the one committed verifier accepts. The CLI path
(nargo, bb) generated that verifier; the browser path (noir_js, bb.js) has to match it. Nothing
connects a Cargo-less JS dependency to zk/circuits/toolchain.txt, so a version bump on either side
would drift silently until a proof failed to verify for reasons no one could see.

Same rule as every other check here: an unreadable input must fail, never register as a match.
"""

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
TOOLCHAIN = ROOT / "zk/circuits/toolchain.txt"
PACKAGE = ROOT / "frontend/package.json"

# JS package -> the toolchain line it must equal.
PAIRS = {
    "@noir-lang/noir_js": "nargo",
    "@aztec/bb.js": "bb",
}


def pinned(tool: str) -> str | None:
    for line in TOOLCHAIN.read_text().splitlines():
        match = re.match(rf"^{re.escape(tool)}\s+(\S+)\s*$", line.strip())
        if match:
            return match.group(1)
    return None


def main() -> int:
    deps = json.loads(PACKAGE.read_text()).get("dependencies", {})
    failed = False

    for package, tool in sorted(PAIRS.items()):
        want = pinned(tool)
        got = deps.get(package)

        if not want:
            print(f"FAIL  {tool}: no version found in zk/circuits/toolchain.txt")
            failed = True
            continue
        if not got:
            print(f"FAIL  {package}: not a dependency of frontend/package.json")
            failed = True
            continue
        if got.startswith(("^", "~", ">", "<", "*")):
            print(f"FAIL  {package} is pinned as {got!r}; a range lets the proving path drift")
            failed = True
            continue
        if got != want:
            print(f"FAIL  {package} is {got}, but toolchain.txt pins {tool} {want}")
            failed = True
            continue

        print(f"ok    {package} {got} matches {tool} {want}")

    if failed:
        print("\nThe browser proving path no longer matches the toolchain that generated the")
        print("committed verifier. Change both, or neither.")
        return 1

    print("\nBrowser proving packages match the pinned toolchain.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
