#!/usr/bin/env python3
"""Fail if the installed Anchor or Solana CLI differs from the versions pinned in solana/Anchor.toml.

A program built by a different Anchor produces a different IDL and can derive accounts differently,
so the IDL drift and layout checks would compare against output from a toolchain nobody pinned.
An unreadable version is a failure, never a match.
"""

import re
import subprocess
import sys
import tomllib
from pathlib import Path

ANCHOR_TOML = Path(__file__).resolve().parent.parent / "solana/Anchor.toml"

TOOLS = {
    "anchor_version": (["anchor", "--version"], r"^anchor-cli (\S+)"),
    "solana_version": (["solana", "--version"], r"^solana-cli (\S+)"),
}


def main():
    pins = tomllib.loads(ANCHOR_TOML.read_text()).get("toolchain", {})
    failed = False
    for key, (cmd, pattern) in TOOLS.items():
        want = pins.get(key)
        if not want:
            print(f"{ANCHOR_TOML}: [toolchain] {key} is not pinned", file=sys.stderr)
            failed = True
            continue
        try:
            out = subprocess.run(cmd, capture_output=True, text=True, check=True).stdout
        except (OSError, subprocess.CalledProcessError) as e:
            print(f"{cmd[0]}: cannot read version: {e}", file=sys.stderr)
            failed = True
            continue
        match = re.search(pattern, out.strip())
        got = match.group(1) if match else None
        if got != want:
            print(f"{cmd[0]} is {got!r}; solana/Anchor.toml pins {want}", file=sys.stderr)
            failed = True
        else:
            print(f"{cmd[0]} {got} matches the pin")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
