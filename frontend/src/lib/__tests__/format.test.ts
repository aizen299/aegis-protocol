import { describe, expect, it } from "vitest";

import { formatTime, formatUnixTime, relativeToNow, shortAddress, shortId } from "../format";
import { formatRaw } from "../units";

describe("formatRaw", () => {
  // The API sends uint256 as a decimal string. It must never become a JS number.
  it("scales a raw string by its own decimals", () => {
    expect(formatRaw("250000000", 6, "tUSD")).toBe("250 tUSD");
  });

  it("handles values beyond Number.MAX_SAFE_INTEGER without losing precision", () => {
    // 10^24 raw at 18 decimals is one million tokens; as a float it would round.
    expect(formatRaw("1000000000000000000000000", 18)).toBe("1,000,000");
  });

  it("returns undefined for a malformed value rather than zero", () => {
    expect(formatRaw("not-a-number", 6)).toBeUndefined();
    expect(formatRaw(undefined, 6)).toBeUndefined();
    expect(formatRaw("1", undefined)).toBeUndefined();
  });

  it("renders a genuine zero as zero", () => {
    expect(formatRaw("0", 6, "tUSD")).toBe("0 tUSD");
  });
});

describe("time formatting", () => {
  it("renders absent timestamps as a dash rather than an epoch", () => {
    expect(formatTime(undefined)).toBe("—");
    expect(formatUnixTime(0)).toBe("—");
    expect(formatUnixTime(undefined)).toBe("—");
    expect(relativeToNow(undefined)).toBe("—");
  });

  it("rejects unparseable timestamps", () => {
    expect(formatTime("not a date")).toBe("—");
  });

  it("describes past and future relative to now", () => {
    const now = Date.parse("2026-01-01T00:00:00Z");
    expect(relativeToNow("2026-01-01T00:00:00Z", now + 120_000)).toBe("2 minutes ago");
    expect(relativeToNow("2026-01-01T02:00:00Z", now)).toBe("in 2 hours");
    expect(relativeToNow("2026-01-02T00:00:00Z", now)).toBe("in 1 day");
  });
});

describe("truncation", () => {
  it("keeps both ends of an address so it stays checkable", () => {
    const address = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";
    const short = shortAddress(address);
    expect(short.startsWith("0xf39F")).toBe(true);
    expect(short.endsWith("2266")).toBe(true);
  });

  it("leaves short values alone", () => {
    expect(shortAddress("0x1234")).toBe("0x1234");
    expect(shortId("0x1234")).toBe("0x1234");
  });
});
