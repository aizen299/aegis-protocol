// Generates a vault-membership proof through the same libraries the browser uses.
//
// The point is not convenience — tools/prove.sh already proves from the CLI. It is that the browser
// path and the CLI path must produce proofs the one committed verifier accepts, and nothing
// enforces that unless something checks it. See docs/v1.3-write-actions-plan.md §2.2.
//
// Lives under frontend/ because it resolves the same node_modules the browser bundle does. A copy
// in tools/ would resolve nothing, or worse, a different version.
//
// Usage: node frontend/tools/prove-browser.mjs <circuit.json> <prover.json> <out-dir>
// Writes <out-dir>/proof.hex and <out-dir>/public_inputs.hex, same shape as tools/prove.sh.

import { readFileSync, writeFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";

import { Noir } from "@noir-lang/noir_js";
import { Barretenberg, BackendType, UltraHonkBackend } from "@aztec/bb.js";

const [circuitPath, inputsPath, outDir] = process.argv.slice(2);
if (!circuitPath || !inputsPath || !outDir) {
  console.error("usage: prove-browser.mjs <circuit.json> <prover.json> <out-dir>");
  process.exit(1);
}

const circuit = JSON.parse(readFileSync(circuitPath, "utf8"));
const inputs = JSON.parse(readFileSync(inputsPath, "utf8"));

const noir = new Noir(circuit);
const { witness } = await noir.execute(inputs);

// BackendType.Wasm is forced. Left to itself, bb.js in Node prefers the native bb binary — which
// would mean this script exercised the CLI path while appearing to test the browser one, and the
// check built on it would prove nothing.
const api = await Barretenberg.new({ backend: BackendType.Wasm });

// verifierTarget "evm" is the same flag tools/gen-verifier.sh passes to the bb CLI, and it has to
// be: it selects a keccak oracle AND leaves ZK enabled. The legacy { keccak: true } option disables
// ZK, producing a smaller proof the committed verifier rejects — a failure that reads as "browser
// proving does not work" rather than "wrong proof variant".
const backend = new UltraHonkBackend(circuit.bytecode, api);
const proof = await backend.generateProof(witness, { verifierTarget: "evm" });

mkdirSync(outDir, { recursive: true });

const hex = (bytes) => Buffer.from(bytes).toString("hex");
writeFileSync(join(outDir, "proof.hex"), `0x${hex(proof.proof)}\n`);
writeFileSync(
  join(outDir, "public_inputs.hex"),
  proof.publicInputs.map((p) => p.toString()).join(" ") + "\n",
);

console.error(`proof bytes: ${proof.proof.length}, public inputs: ${proof.publicInputs.length}`);
await api.destroy();
