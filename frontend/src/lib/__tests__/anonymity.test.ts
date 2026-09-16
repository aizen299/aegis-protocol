import { describe, expect, it } from "vitest";

import { assessAnonymitySet } from "../anonymity";

describe("assessAnonymitySet", () => {
  // The case the v0.4 plan calls out: at N=1, "one of N" is nothing at all, and a UI that renders
  // a bare "1" implies a guarantee the protocol does not make.
  it("says a single commitment provides no anonymity, in those words", () => {
    const verdict = assessAnonymitySet(1);
    expect(verdict.tone).toBe("none");
    expect(verdict.headline).toMatch(/no anonymity/i);
    expect(verdict.detail).toMatch(/only one/i);
  });

  it("distinguishes an empty tree from a tree of one", () => {
    expect(assessAnonymitySet(0).headline).toMatch(/no commitments/i);
    expect(assessAnonymitySet(0).headline).not.toEqual(assessAnonymitySet(1).headline);
  });

  it("calls a small set weak rather than reporting a number alone", () => {
    for (const n of [2, 5, 9, 10, 99]) {
      const verdict = assessAnonymitySet(n);
      expect(verdict.tone).toBe("weak");
      expect(verdict.detail.length).toBeGreaterThan(40);
    }
  });

  // Even a large set is never described as private outright — the set is the whole guarantee.
  it("never claims a set is safe or private", () => {
    for (const n of [0, 1, 5, 50, 500, 100_000]) {
      const verdict = assessAnonymitySet(n);
      const text = `${verdict.headline} ${verdict.detail}`.toLowerCase();
      expect(text).not.toMatch(/\b(safe|secure|private|anonymous)\b/);
    }
  });

  it("names the actual count so a reader can judge for themselves", () => {
    expect(assessAnonymitySet(42).headline).toContain("42");
    expect(assessAnonymitySet(5000).headline).toContain("5000");
  });
});
