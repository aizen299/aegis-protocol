import { afterEach, describe, expect, it, vi } from "vitest";

import {
  CommitmentNotFound,
  assembleWitness,
  fetchAllLeaves,
  type CommitmentPage,
} from "../assemble";
import { commitmentOf, nullifierOf } from "../witness";

const SECRET = "424242";
const domain = {
  actionId: "0x0000000000000000000000000000000000000000000000000000000000000007" as const,
  chainId: 31337,
  gate: "0x09635F643e140090A9A8Dcd712eD6285858ceBef" as const,
  submitter: "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266" as const,
};

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("assembleWitness", () => {
  it("finds the commitment among the leaves and binds the submitter", async () => {
    const mine = await commitmentOf(424242n);
    const leaves = [0xabcdefn, mine, 0xabcdf2n];

    const assembled = await assembleWitness({ ...domain, secret: SECRET, leaves });

    expect(assembled.leafIndex).toBe(1);
    expect(assembled.inputs.submitter).toBe(BigInt(domain.submitter).toString());
    expect(assembled.inputs.path_elements).toHaveLength(20);
  });

  it("refuses a secret with no matching deposit, rather than proving something else", async () => {
    await expect(
      assembleWitness({ ...domain, secret: "999", leaves: [0xabcdefn] }),
    ).rejects.toBeInstanceOf(CommitmentNotFound);
  });

  // The submitter binds who may present the proof; it must never change which nullifier is spent.
  it("derives the same nullifier whoever the submitter is", async () => {
    const leaves = [await commitmentOf(424242n)];
    const a = await assembleWitness({ ...domain, secret: SECRET, leaves });
    const b = await assembleWitness({
      ...domain,
      secret: SECRET,
      leaves,
      submitter: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
    });
    expect(a.nullifier).toBe(b.nullifier);
  });
});

describe("privacy", () => {
  // docs/v1.3-write-actions-plan.md §3: no secret leaves the browser. Every request made while
  // assembling a witness is recorded, and none may carry the secret, the commitment (which
  // identifies the deposit), or the nullifier before it is deliberately submitted.
  it("sends nothing identifying while assembling a witness", async () => {
    const requests: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: RequestInfo | URL, init?: RequestInit) => {
        requests.push(`${String(url)} ${init?.body ? String(init.body) : ""}`);
        return new Response(JSON.stringify({ items: [] }));
      }),
    );

    const commitment = await commitmentOf(424242n);
    const leaves = [0xabcdefn, commitment, 0xabcdf2n];
    const pages: CommitmentPage[] = [
      leaves.map((leaf, leafIndex) => ({ leafIndex, commitment: `0x${leaf.toString(16)}` })),
    ];

    const fetched = await fetchAllLeaves(async (limit, offset) => {
      await fetch(`http://api.test/v1/zk/commitments?limit=${limit}&offset=${offset}`);
      return offset === 0 ? (pages[0] ?? []) : [];
    }, leaves.length);

    const assembled = await assembleWitness({ ...domain, secret: SECRET, leaves: fetched });

    const nullifier = await nullifierOf(
      424242n,
      BigInt(domain.actionId),
      BigInt(domain.chainId),
      BigInt(domain.gate),
    );

    const forbidden = [
      SECRET,
      commitment.toString(),
      commitment.toString(16),
      nullifier.toString(),
      nullifier.toString(16),
      assembled.inputs.secret as string,
    ];

    expect(requests.length).toBeGreaterThan(0);
    for (const request of requests) {
      for (const value of forbidden) {
        expect(request.toLowerCase()).not.toContain(value.toLowerCase());
      }
    }
  });
});

describe("fetchAllLeaves", () => {
  const page = (from: number, count: number): CommitmentPage =>
    Array.from({ length: count }, (_, i) => ({
      leafIndex: from + i,
      commitment: `0x${(from + i + 1).toString(16)}`,
    }));

  it("pages until a short page and returns every leaf in order", async () => {
    const leaves = await fetchAllLeaves(
      async (limit, offset) => (offset === 0 ? page(0, limit) : offset === 2 ? page(2, 1) : []),
      3,
      2,
    );
    expect(leaves).toEqual([1n, 2n, 3n]);
  });

  // A tree with a hole has a different root. Proving against it wastes twenty seconds and fails.
  it("refuses a gap in the indexed leaves", async () => {
    await expect(
      fetchAllLeaves(async () => [
        { leafIndex: 0, commitment: "0x1" },
        { leafIndex: 2, commitment: "0x3" },
      ], 3),
    ).rejects.toThrow(/gap at leaf 1/);
  });

  it("refuses an indexer that is behind the chain", async () => {
    await expect(fetchAllLeaves(async () => page(0, 2), 5)).rejects.toThrow(/2 of the chain's 5/);
  });

  it("passes only a limit and an offset to the fetcher", async () => {
    const fetcher = vi.fn(async () => []);
    await fetchAllLeaves(fetcher, 0);
    expect(fetcher).toHaveBeenCalledWith(100, 0);
    expect(fetcher.mock.calls[0]).toHaveLength(2);
  });
});
