import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

import {
  FIELD_SIZE,
  commitmentOf,
  merklePath,
  nullifierOf,
  toBytes32,
  toField,
  zeroSubtrees,
} from "../witness";

// The committed proof fixture is what the contract suite verifies against the generated verifier,
// so agreeing with it means agreeing with the circuit and the contract at once. Read at test time:
// a regenerated fixture is tracked, never stale.
function fixture(name: string): bigint {
  const path = resolve(__dirname, "../../../../../contracts/test/utils/ProofFixture.sol");
  const match = readFileSync(path, "utf8").match(new RegExp(`${name} = bytes32\\((0x[0-9a-f]+)\\)`));
  if (!match?.[1]) throw new Error(`ProofFixture has no ${name}`);
  return BigInt(match[1]);
}

// The fixture's domain: secret 424242 at leaf 0 of an otherwise empty tree. See tools/poseidon.
const SECRET = 424242n;

describe("witness builder agrees with the committed proof fixture", () => {
  it("rebuilds the fixture's root from a single commitment", async () => {
    const { root } = await merklePath([await commitmentOf(SECRET)], 0);
    expect(toBytes32(root)).toBe(toBytes32(fixture("ROOT")));
  });

  it("derives the fixture's nullifier from the same domain", async () => {
    const nullifier = await nullifierOf(
      SECRET,
      fixture("ACTION_ID"),
      fixture("CHAIN_ID"),
      fixture("GATE"),
    );
    expect(toBytes32(nullifier)).toBe(toBytes32(fixture("NULLIFIER_HASH")));
  });
});

// Roots read from CommitmentTree.sol on a local chain after inserting exactly these leaves, in order.
// A single-leaf fixture only exercises left children and zero siblings; these cover right children
// and real siblings, which is where an incremental tree and a rebuilt one most often disagree.
// Regenerate by inserting the leaves into a fresh tree and reading currentRoot() after each.
const LEAVES = [0xabcdefn, 0xabcdf1n, 0xabcdf2n];
const CONTRACT_ROOTS = [
  "0x291c6ac047248b801611c91c82e0c77ca680cba097eac180871a635a3f0111ca",
  "0x0a39ccc54e9cb15d9c9cfc223a33186dcb53efa24ccce94ecedb1a01c3cb2213",
  "0x20cf04f7c627dced2b206f610b87a184f16adfc6096140edd22b5035bd83795b",
];

describe("witness builder agrees with the contract's incremental tree", () => {
  it("matches the root after each insertion", async () => {
    for (let n = 1; n <= LEAVES.length; n++) {
      const { root } = await merklePath(LEAVES.slice(0, n), n - 1);
      expect(toBytes32(root)).toBe(CONTRACT_ROOTS[n - 1]);
    }
  });

  it("gives every leaf in a tree the same root", async () => {
    const roots = await Promise.all(LEAVES.map((_, i) => merklePath(LEAVES, i)));
    expect(new Set(roots.map((r) => r.root)).size).toBe(1);
  });

  it("marks a right child's position as 1", async () => {
    const { pathIndices } = await merklePath(LEAVES, 1);
    expect(pathIndices[0]).toBe(1);
  });
});

describe("witness builder refuses malformed input", () => {
  it("rejects a leaf index outside the tree", async () => {
    await expect(merklePath([1n], 1)).rejects.toThrow();
    await expect(merklePath([1n], -1)).rejects.toThrow();
  });

  it("rejects values outside the field", () => {
    expect(() => toField(FIELD_SIZE)).toThrow();
    expect(() => toField(-1n)).toThrow();
    expect(toField("7")).toBe(7n);
  });

  it("computes the zero subtrees from the contract's seed", async () => {
    const zeros = await zeroSubtrees();
    expect(zeros).toHaveLength(21);
    expect(zeros[0]).toBeLessThan(FIELD_SIZE);
  });
});
