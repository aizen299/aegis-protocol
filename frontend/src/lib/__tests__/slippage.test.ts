import { describe, expect, it } from "vitest";

import {
  DEFAULT_TOLERANCE_BPS,
  MAX_TOLERANCE_BPS,
  formatTolerance,
  minimumOut,
} from "../slippage";

describe("minimumOut", () => {
  it("applies the tolerance in basis points", () => {
    expect(minimumOut(10_000n, 50)).toBe(9_950n);
    expect(minimumOut(1_000_000n, 100)).toBe(990_000n);
  });

  // Rounding up would put the floor above what the quoted rate produces, so a fill at exactly the
  // expected price would revert. The direction matters more than the precision.
  it("rounds the floor down, never up", () => {
    expect(minimumOut(3n, 50)).toBeLessThanOrEqual(3n);
    expect(minimumOut(1n, 1)).toBe(0n);
    expect(minimumOut(199n, 50)).toBe(198n);
  });

  it("a zero tolerance floors at exactly the expected amount", () => {
    expect(minimumOut(12_345n, 0)).toBe(12_345n);
  });

  it("stays exact on values beyond Number.MAX_SAFE_INTEGER", () => {
    const huge = 10n ** 30n;
    expect(minimumOut(huge, 50)).toBe((huge * 9_950n) / 10_000n);
  });

  it("returns undefined rather than a wrong floor for an absent amount", () => {
    expect(minimumOut(undefined, 50)).toBeUndefined();
  });

  // A nonsense tolerance must not silently become a permissive floor — that would be worse than
  // no floor, because the UI would claim one.
  it("refuses a tolerance that is negative, fractional, or beyond the cap", () => {
    for (const bad of [-1, 0.5, MAX_TOLERANCE_BPS + 1, Number.NaN, 10_001]) {
      expect(minimumOut(10_000n, bad)).toBeUndefined();
    }
  });

  it("the default tolerance is inside the cap and not zero", () => {
    expect(DEFAULT_TOLERANCE_BPS).toBeGreaterThan(0);
    expect(DEFAULT_TOLERANCE_BPS).toBeLessThanOrEqual(MAX_TOLERANCE_BPS);
  });
});

describe("formatTolerance", () => {
  it("renders basis points as a percentage a reader recognises", () => {
    expect(formatTolerance(50)).toBe("0.5%");
    expect(formatTolerance(100)).toBe("1%");
    expect(formatTolerance(25)).toBe("0.25%");
  });
});
