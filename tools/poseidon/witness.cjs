// Builds a witness for zk/circuits/vault_membership and prints it as a Nargo Prover.toml.
//
// The tree it models is the real one: a single commitment at leaf 0 of an otherwise-empty
// CommitmentTree, whose zero subtrees are keccak256("aegis.commitment-tree.empty") % FIELD_SIZE
// hashed upward. That the deployed contract reports the same root is asserted in
// contracts/test/unit/ZkVaultGate.t.sol — the JS, the circuit, and the contract have to agree.
const { buildPoseidon } = require("circomlibjs");
const { keccak256, toUtf8Bytes } = require("ethers");

const FIELD_SIZE =
  21888242871839275222246405745257275088548364400416034343698204186575808495617n;

const DEPTH = 20;
const SECRET = BigInt(process.env.SECRET || "424242");
const ACTION = BigInt(process.env.ACTION_ID || "7");
const CHAIN = BigInt(process.env.CHAIN_ID || "31337");
const GATE = BigInt(process.env.GATE || "0xab");

(async () => {
  const p = await buildPoseidon();
  const F = p.F;
  const h = (xs) => F.toObject(p(xs));

  const commitment = h([SECRET]);

  // The contract's zero subtrees, recomputed here rather than copied.
  let zero = BigInt(keccak256(toUtf8Bytes("aegis.commitment-tree.empty"))) % FIELD_SIZE;
  const zeros = [];
  for (let i = 0; i < DEPTH; i++) {
    zeros.push(zero);
    zero = h([zero, zero]);
  }

  // Leaf 0 of an otherwise-empty tree: every sibling is the zero subtree, every index is left.
  let root = commitment;
  for (let i = 0; i < DEPTH; i++) {
    root = h([root, zeros[i]]);
  }

  const nullifier = h([h([SECRET, ACTION]), h([CHAIN, GATE])]);
  const q = (x) => `"${x.toString()}"`;

  console.log(`root = ${q(root)}`);
  console.log(`nullifier_hash = ${q(nullifier)}`);
  console.log(`action_id = ${q(ACTION)}`);
  console.log(`chain_id = ${q(CHAIN)}`);
  console.log(`gate = ${q(GATE)}`);
  console.log(`secret = ${q(SECRET)}`);
  console.log(`path_elements = [${zeros.map(q).join(", ")}]`);
  console.log(`path_indices = [${Array(DEPTH).fill(0).map(q).join(", ")}]`);
})();
