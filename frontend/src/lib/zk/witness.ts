import { buildPoseidon } from "circomlibjs";
import { keccak256, toBytes } from "viem";

// Builds the circuit's inputs in the browser, so the secret never leaves it.
//
// Every value here has to agree with two other implementations: the Noir circuit, which recomputes
// the commitment and nullifier, and CommitmentTree.sol, which computes the zero subtrees and roots.
// circomlibjs is pinned to the exact version tools/poseidon uses — the one that generated the
// fixtures the contract suite verifies — and witness.test.ts checks this file against those
// fixtures, so a drift fails there rather than as a proof no root ever matches.

export const FIELD_SIZE =
  21888242871839275222246405745257275088548364400416034343698204186575808495617n;

export const TREE_DEPTH = 20;

type Hasher = (inputs: bigint[]) => bigint;

let hasher: Promise<Hasher> | undefined;

export function poseidon(): Promise<Hasher> {
  hasher ??= buildPoseidon().then((p) => (inputs: bigint[]) => p.F.toObject(p(inputs)));
  return hasher;
}

export function toField(value: string | bigint): bigint {
  const n = typeof value === "bigint" ? value : BigInt(value);
  if (n < 0n || n >= FIELD_SIZE) throw new Error("value is not a BN254 field element");
  return n;
}

export async function commitmentOf(secret: bigint): Promise<bigint> {
  const h = await poseidon();
  return h([secret]);
}

export async function nullifierOf(
  secret: bigint,
  actionId: bigint,
  chainId: bigint,
  gate: bigint,
): Promise<bigint> {
  const h = await poseidon();
  return h([h([secret, actionId]), h([chainId, gate])]);
}

// CommitmentTree.sol computes these the same way: keccak of a fixed string, reduced into the field,
// hashed upward. Recomputed rather than copied, so a change to the contract's seed is caught.
export async function zeroSubtrees(): Promise<bigint[]> {
  const h = await poseidon();
  let zero = BigInt(keccak256(toBytes("aegis.commitment-tree.empty"))) % FIELD_SIZE;
  const zeros: bigint[] = [];
  for (let i = 0; i <= TREE_DEPTH; i++) {
    zeros.push(zero);
    zero = h([zero, zero]);
  }
  return zeros;
}

export type MerklePath = {
  root: bigint;
  pathElements: bigint[];
  pathIndices: number[];
};

/// The path from `leafIndex` to the root of a tree holding exactly `leaves`, in leaf order.
///
/// Built from every leaf, level by level, with absent nodes filled from the zero subtrees — the same
/// shape the contract's incremental insert produces. A missing or reordered leaf yields a different
/// root, which the caller checks against the chain before proving.
export async function merklePath(leaves: bigint[], leafIndex: number): Promise<MerklePath> {
  if (!Number.isInteger(leafIndex) || leafIndex < 0 || leafIndex >= leaves.length) {
    throw new Error("the leaf index is outside the tree");
  }
  if (leaves.length > 2 ** TREE_DEPTH) throw new Error("more leaves than the tree can hold");

  const h = await poseidon();
  const zeros = await zeroSubtrees();

  let level = leaves.slice();
  let index = leafIndex;
  const pathElements: bigint[] = [];
  const pathIndices: number[] = [];

  const at = (values: bigint[], i: number): bigint => {
    const value = values[i];
    if (value === undefined) throw new Error("the tree is malformed");
    return value;
  };

  for (let depth = 0; depth < TREE_DEPTH; depth++) {
    const zero = at(zeros, depth);
    const isRight = index % 2 === 1;
    const siblingIndex = isRight ? index - 1 : index + 1;
    pathElements.push(siblingIndex < level.length ? at(level, siblingIndex) : zero);
    pathIndices.push(isRight ? 1 : 0);

    const next: bigint[] = [];
    for (let i = 0; i < level.length; i += 2) {
      const right = i + 1 < level.length ? at(level, i + 1) : zero;
      next.push(h([at(level, i), right]));
    }
    level = next;
    index = Math.floor(index / 2);
  }

  return { root: at(level, 0), pathElements, pathIndices };
}

export function toBytes32(value: bigint): `0x${string}` {
  return `0x${value.toString(16).padStart(64, "0")}`;
}
