#!/usr/bin/env python3
"""Renders a bb proof and its public inputs into the Solidity fixture the contract suite uses."""
import sys
import pathlib

proof_path, inputs_path, out_path = sys.argv[1], sys.argv[2], sys.argv[3]

proof = pathlib.Path(proof_path).read_bytes()
raw_inputs = pathlib.Path(inputs_path).read_bytes()

if not proof or len(raw_inputs) % 32 != 0:
    sys.exit("render-proof-fixture: proof or public inputs are malformed")

fields = [raw_inputs[i : i + 32].hex() for i in range(0, len(raw_inputs), 32)]
if len(fields) != 5:
    sys.exit(f"render-proof-fixture: expected 5 public inputs, got {len(fields)}")

names = ["ROOT", "NULLIFIER_HASH", "ACTION_ID", "CHAIN_ID", "GATE"]
constants = "\n".join(
    f"    bytes32 internal constant {name} = bytes32(0x{value});"
    for name, value in zip(names, fields)
)
assignments = "\n".join(f"        inputs[{i}] = {name};" for i, name in enumerate(names))

pathlib.Path(out_path).write_text(f"""// SPDX-License-Identifier: MIT
pragma solidity 0.8.28;

// GENERATED FILE — DO NOT EDIT.
//
// A real proof for zk/circuits/vault_membership, produced by tools/gen-proof-fixture.sh from the
// pinned toolchain. Committed so the contract suite can verify a genuine proof without needing
// nargo or bb on PATH, the same reason the Poseidon bytecode is committed.
//
// Regenerate with `make zk-proof-fixture`.
library ProofFixture {{
    // slither-disable-next-line too-many-digits
    bytes internal constant PROOF = hex"{proof.hex()}";

    // The circuit's five public inputs, in declaration order.
{constants}

    function publicInputs() internal pure returns (bytes32[] memory inputs) {{
        inputs = new bytes32[](5);
{assignments}
    }}
}}
""")
