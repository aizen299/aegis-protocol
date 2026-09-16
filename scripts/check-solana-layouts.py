#!/usr/bin/env python3
"""Fail if a Solana account layout changes other than by carving new fields from its reserved tail.

The Solana counterpart of `make contracts-layout-check`. An upgraded program reads accounts written by
the old one, so a field inserted, removed, retyped, or moved reinterprets live data. The only safe
change is to take bytes from the trailing `reserved` array: every existing field keeps its offset and
the account keeps its size, so nothing needs reallocating.

Variable-size types are refused outright: with borsh, an Option or Vec shifts every later field's
offset depending on its value, so no fixed layout exists to check.

Usage: check-solana-layouts.py IDL BASELINE [--write]
"""

import json
import sys
from pathlib import Path

DISCRIMINATOR_LEN = 8
PRIMITIVE = {"u8": 1, "i8": 1, "bool": 1, "u16": 2, "i16": 2, "u32": 4, "i32": 4,
             "u64": 8, "i64": 8, "u128": 16, "i128": 16, "pubkey": 32}


class LayoutError(Exception):
    pass


def size_of(ty, where):
    if isinstance(ty, str) and ty in PRIMITIVE:
        return PRIMITIVE[ty], ty
    if isinstance(ty, dict) and set(ty) == {"array"}:
        inner, count = ty["array"]
        if not isinstance(count, int) or count <= 0:
            raise LayoutError(f"{where}: array length {count!r} is not a fixed positive integer")
        size, name = size_of(inner, where)
        return size * count, f"[{name};{count}]"
    raise LayoutError(f"{where}: type {json.dumps(ty)} has no fixed size")


def layouts(idl):
    types = {t["name"]: t for t in idl.get("types", [])}
    out = {}
    for account in idl.get("accounts", []):
        name = account["name"]
        definition = types.get(name)
        if definition is None or definition["type"].get("kind") != "struct":
            raise LayoutError(f"{name}: no struct definition in the IDL")
        fields, offset = [], DISCRIMINATOR_LEN
        for field in definition["type"]["fields"]:
            size, ty = size_of(field["type"], f"{name}.{field['name']}")
            fields.append({"name": field["name"], "type": ty, "offset": offset, "size": size})
            offset += size
        if not fields or fields[-1]["name"] != "reserved" or not fields[-1]["type"].startswith("[u8;"):
            raise LayoutError(f"{name}: the last field must be `reserved: [u8; N]`, the space later fields are carved from")
        out[name] = {"discriminator": account["discriminator"], "size": offset, "fields": fields}
    if not out:
        raise LayoutError("the IDL declares no accounts")
    return out


def compare(baseline, current):
    errors = []
    for name, base in baseline.items():
        cur = current.get(name)
        if cur is None:
            errors.append(f"{name}: removed; live accounts of this type would become unreadable")
            continue
        if cur["discriminator"] != base["discriminator"]:
            errors.append(f"{name}: discriminator changed")
        if cur["size"] != base["size"]:
            errors.append(f"{name}: size {base['size']} -> {cur['size']}; new fields must come from `reserved`")

        kept = base["fields"][:-1]
        for i, field in enumerate(kept):
            if i >= len(cur["fields"]) - 1 or cur["fields"][i] != field:
                got = cur["fields"][i] if i < len(cur["fields"]) else None
                errors.append(f"{name}: field {i} was {field}, now {got}")
                break
        else:
            reserved = base["fields"][-1]
            added = cur["fields"][len(kept):-1]
            if added and added[0]["offset"] != reserved["offset"]:
                errors.append(f"{name}: new fields start at {added[0]['offset']}, not at reserved's {reserved['offset']}")
            if any(f["name"] == "reserved" for f in added):
                errors.append(f"{name}: `reserved` must stay the last field")

    for name in current:
        if name not in baseline:
            errors.append(f"{name}: no baseline; record one with --write once the layout is final")
    return errors


def main(argv):
    args = [a for a in argv if a != "--write"]
    if len(args) != 2:
        print(__doc__.strip().splitlines()[-1], file=sys.stderr)
        return 2
    idl_path, baseline_path = map(Path, args)

    try:
        current = layouts(json.loads(idl_path.read_text()))
    except (OSError, ValueError, KeyError, LayoutError) as e:
        print(f"cannot read layouts from {idl_path}: {e}", file=sys.stderr)
        return 1

    if "--write" in argv:
        baseline_path.parent.mkdir(parents=True, exist_ok=True)
        baseline_path.write_text(json.dumps(current, indent=2) + "\n")
        print(f"wrote {baseline_path}")
        return 0

    try:
        baseline = json.loads(baseline_path.read_text())
    except (OSError, ValueError) as e:
        print(f"cannot read baseline {baseline_path}: {e}", file=sys.stderr)
        return 1
    if not baseline:
        print(f"baseline {baseline_path} is empty", file=sys.stderr)
        return 1

    errors = compare(baseline, current)
    for e in errors:
        print(f"LAYOUT: {e}", file=sys.stderr)
    if errors:
        return 1
    print(f"account layouts match {baseline_path}: {', '.join(sorted(current))}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
