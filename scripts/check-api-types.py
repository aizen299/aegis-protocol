#!/usr/bin/env python3
"""Fail if the frontend's API types drift from the Go structs they mirror.

The frontend hand-writes TypeScript for every response shape. Nothing connects the two, so a field
renamed in Go stays compiling in TypeScript and fails at runtime as an undefined — which is how
fetchTvl came to be typed `{ tvl: string }` against an endpoint returning an object.

Same rule the ABI and layout checks follow: an empty read must never register as a match.
"""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
GO_TYPES = ROOT / "backend/pkg/types"
TS_API = ROOT / "frontend/src/lib/api.ts"

# TypeScript name -> Go name.
MIRRORED = {
    "OracleFeed": "OracleFeed",
    "OracleRound": "OracleRound",
    "OracleSubmission": "OracleSubmission",
    "OracleNode": "OracleNode",
    "Proposal": "Proposal",
    "Vote": "Vote",
    "GovernorMetadata": "GovernorMetadata",
    "ProposalAction": "ProposalAction",
    "ZkGateMetadata": "ZkGateMetadata",
    "Commitment": "Commitment",
    "ZkAction": "ZkAction",
    "PrivateAction": "PrivateAction",
    "AnonymitySet": "AnonymitySet",
    "RemoteAction": "RemoteAction",
}


def go_fields(name: str) -> set[str]:
    for path in sorted(GO_TYPES.glob("*.go")):
        source = path.read_text()
        match = re.search(rf"^type {re.escape(name)} struct \{{(.*?)^\}}", source, re.S | re.M)
        if not match:
            continue
        fields = set()
        for tag in re.finditer(r'json:"([^"]+)"', match.group(1)):
            key = tag.group(1).split(",")[0]
            if key and key != "-":
                fields.add(key)
        return fields
    return set()


def ts_fields(source: str, name: str) -> set[str] | None:
    match = re.search(rf"^export type {re.escape(name)} = \{{(.*?)^\}};", source, re.S | re.M)
    if not match:
        return None
    return {m.group(1) for m in re.finditer(r"^\s{2}(\w+)\??:", match.group(1), re.M)}


def main() -> int:
    source = TS_API.read_text()
    failed = False

    for ts_name, go_name in sorted(MIRRORED.items()):
        expected = go_fields(go_name)
        actual = ts_fields(source, ts_name)

        if not expected:
            print(f"FAIL  {go_name}: no Go struct found, or it declares no json tags")
            failed = True
            continue
        if actual is None:
            print(f"FAIL  {ts_name}: no such type in frontend/src/lib/api.ts")
            failed = True
            continue

        missing = expected - actual
        invented = actual - expected
        if missing or invented:
            failed = True
            print(f"FAIL  {ts_name} does not mirror types.{go_name}")
            if missing:
                print(f"        missing in TypeScript: {', '.join(sorted(missing))}")
            if invented:
                print(f"        not present in Go:     {', '.join(sorted(invented))}")
        else:
            print(f"ok    {ts_name} mirrors types.{go_name} ({len(expected)} fields)")

    if failed:
        print("\nThe frontend's API types have drifted from the backend's.")
        return 1

    print("\nFrontend API types mirror the backend.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
