// Generates the Poseidon contract artifact consumed by contracts/src/zk.
//
// Poseidon is a cryptographic primitive: it is generated from circomlib and never hand-written,
// the same rule docs/zk.md applies to the verifier. The circuit uses Noir's circomlib-compatible
// Poseidon, so the two agree by construction — and contracts/test/unit/Poseidon.t.sol proves it
// against the same vectors rather than trusting that sentence.
//
// Node is required only to run this. The contracts build and test path never needs it: the output
// is committed Solidity.

const fs = require("fs");
const path = require("path");
const { poseidonContract } = require("circomlibjs");

const outDir = process.argv[2] || path.join(__dirname, "../../contracts/src/zk/poseidon");
const version = JSON.parse(
  fs.readFileSync(path.join(__dirname, "node_modules/circomlibjs/package.json"), "utf8"),
).version;

// (inputs, name). 1 input builds the commitment; 2 build the Merkle path and the nullifier.
const VARIANTS = [
  [1, "PoseidonT2"],
  [2, "PoseidonT3"],
];

const parts = [
  "// SPDX-License-Identifier: MIT",
  "pragma solidity 0.8.28;",
  "",
  "// GENERATED FILE — DO NOT EDIT.",
  "//",
  `// Generated from circomlibjs ${version} by tools/poseidon/generate.js.`,
  "// Regenerate with `make poseidon-gen`; `make poseidon-check` fails if this file has drifted.",
  "//",
  "// Poseidon is a cryptographic primitive and is generated, never hand-written — the same rule",
  "// docs/zk.md applies to the verifier. A single wrong round constant yields a tree that no proof",
  "// can ever verify against, and the failure appears only at integration.",
  "",
];

for (const [inputs, name] of VARIANTS) {
  const code = poseidonContract.createCode(inputs);
  parts.push(
    `/// @notice Creation code for a Poseidon accepting ${inputs} field element${inputs === 1 ? "" : "s"}.`,
    `library ${name}Bytecode {`,
    "    // Generated contract bytecode is a long hex literal by definition. The detector is looking",
    "    // for a magic number a human typed, which is the opposite of this.",
    "    // slither-disable-next-line too-many-digits",
    `    bytes internal constant CREATION_CODE = hex"${code.replace(/^0x/, "")}";`,
    "}",
    "",
  );
}

fs.mkdirSync(outDir, { recursive: true });
const outFile = path.join(outDir, "PoseidonBytecode.sol");
fs.writeFileSync(outFile, parts.join("\n"));

console.log(`wrote ${outFile} from circomlibjs ${version}`);
