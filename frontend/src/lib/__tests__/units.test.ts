import { describe, expect, it } from "vitest";

import { formatAmount, parseAmount, shareDecimals } from "../units";

describe("formatAmount", () => {
  // The whole reason decimals are asset metadata rather than a constant: a six-decimal token
  // rendered as eighteen is wrong by a factor of a trillion.
  it("renders a six-decimal amount as its asset, not as ether", () => {
    expect(formatAmount(250_000_000n, 6, "tUSD")).toBe("250 tUSD");
    expect(formatAmount(250_000_000n, 18, "tUSD")).not.toBe("250 tUSD");
  });

  it("renders zero as zero rather than as absent", () => {
    expect(formatAmount(0n, 6, "tUSD")).toBe("0 tUSD");
  });

  it("returns undefined when the value or its decimals are missing", () => {
    expect(formatAmount(undefined, 6)).toBeUndefined();
    expect(formatAmount(250_000_000n, undefined)).toBeUndefined();
  });
});

describe("shareDecimals", () => {
  it("adds the virtual-shares offset to the asset's decimals", () => {
    expect(shareDecimals(6, 3)).toBe(9);
  });

  it("is undefined when either input is", () => {
    expect(shareDecimals(undefined, 3)).toBeUndefined();
    expect(shareDecimals(6, undefined)).toBeUndefined();
  });
});

describe("parseAmount", () => {
  it("parses against the asset's decimals", () => {
    expect(parseAmount("250", 6)).toBe(250_000_000n);
  });

  it("rejects zero, blank, and unparseable input", () => {
    expect(parseAmount("0", 6)).toBeNull();
    expect(parseAmount("   ", 6)).toBeNull();
    expect(parseAmount("abc", 6)).toBeNull();
  });
});
