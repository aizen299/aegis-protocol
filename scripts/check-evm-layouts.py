#!/usr/bin/env python3
"""Fail if a contract's storage layout diverges from its released baseline.

The layout is append-only across UUPS upgrades: a variable moved, retyped, removed, or reordered reads
another variable's slot on the live proxy. One change is allowed, the standard one: new variables
taken from the trailing `__gap`, with every existing variable keeping its slot, offset, and type, and
the gap shrinking so that it still ends where it did. docs/v2.0-solana-plan.md §16.8.

AST ids are stripped from struct, contract, and enum types: they shift when unrelated files join the
compilation unit. Array lengths are kept, since a gap that changes size is a real layout change.

An unreadable or empty layout is an error, never a match.

Usage: check-evm-layouts.py BASELINE CURRENT
  BASELINE  a recorded contracts/deployments/layouts/*.json
  CURRENT   `forge inspect <Contract> storage-layout --json` output
"""

import json
import re
import sys

AST_ID = re.compile(r"(t_(?:struct|contract|enum)\([^)]*\))\d+")
GAP_TYPE = re.compile(r"^t_array\(t_uint256\)(\d+)_storage$")


class LayoutError(Exception):
    pass


def entries(path):
    try:
        with open(path) as f:
            storage = json.load(f).get("storage")
    except (OSError, ValueError) as e:
        raise LayoutError(f"{path}: cannot read: {e}")
    if not storage:
        raise LayoutError(f"{path}: no storage entries; run 'forge clean' and try again")
    return [
        {"label": e["label"], "slot": int(e["slot"]), "offset": int(e["offset"]), "type": AST_ID.sub(r"\1", e["type"])}
        for e in storage
    ]


def gap_length(entry):
    if entry["label"] != "__gap":
        return None
    match = GAP_TYPE.match(entry["type"])
    return int(match.group(1)) if match else None


def compare(baseline, current):
    if baseline == current:
        return []

    base_gap = gap_length(baseline[-1])
    if base_gap is None:
        return ["the baseline has no trailing __gap, so nothing may change"]

    kept = baseline[:-1]
    if current[: len(kept)] != kept:
        for i, (b, c) in enumerate(zip(kept, current)):
            if b != c:
                return [f"entry {i} was {b}, now {c}: existing variables must keep their slot, offset, and type"]
        return ["existing variables were removed"]

    cur_gap = gap_length(current[-1])
    if cur_gap is None:
        return ["the current layout does not end in __gap: new variables must be carved from it, not appended after it"]

    base_start, cur_start = baseline[-1]["slot"], current[-1]["slot"]
    errors = []
    if cur_start + cur_gap != base_start + base_gap:
        errors.append(
            f"__gap ended at slot {base_start + base_gap}, now {cur_start + cur_gap}: the gap must shrink by exactly what was added"
        )
    for added in current[len(kept):-1]:
        if not base_start <= added["slot"] < cur_start:
            errors.append(f"{added['label']} at slot {added['slot']} is outside the old gap [{base_start}, {cur_start})")
    return errors


def main(argv):
    if len(argv) != 2:
        print(__doc__.strip().splitlines()[-3], file=sys.stderr)
        return 2
    try:
        baseline, current = entries(argv[0]), entries(argv[1])
    except LayoutError as e:
        print(f"LAYOUT UNREADABLE: {e}", file=sys.stderr)
        return 1
    errors = compare(baseline, current)
    for e in errors:
        print(f"STORAGE LAYOUT DIVERGED: {e}", file=sys.stderr)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
