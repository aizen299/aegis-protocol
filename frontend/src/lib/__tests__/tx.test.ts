import { describe, expect, it } from "vitest";

import { txPhase } from "../tx";

const hash = `0x${"ab".repeat(32)}` as const;
const base = { signing: false, receiptPending: false };

describe("txPhase", () => {
  it("is idle before anything is sent", () => {
    expect(txPhase(base).kind).toBe("idle");
  });

  it("is signing while the wallet prompt is open", () => {
    expect(txPhase({ ...base, signing: true }).kind).toBe("signing");
  });

  // The defect this module fixes: a hash exists from broadcast, long before inclusion.
  it("treats a hash with no receipt as pending, not as success", () => {
    expect(txPhase({ ...base, hash, receiptPending: true }).kind).toBe("pending");
  });

  it("treats a hash with no receipt status as pending even when nothing claims to be loading", () => {
    expect(txPhase({ ...base, hash, receiptPending: false }).kind).toBe("pending");
  });

  // wagmi resolves a reverted transaction rather than erroring, so this is the case a UI that only
  // checks for errors reports as success.
  it("reports a mined-but-reverted transaction as reverted, never confirmed", () => {
    const phase = txPhase({ ...base, hash, receiptStatus: "reverted" });
    expect(phase.kind).toBe("reverted");
    expect(phase.kind).not.toBe("confirmed");
  });

  it("reports a successful receipt as confirmed", () => {
    expect(txPhase({ ...base, hash, receiptStatus: "success" }).kind).toBe("confirmed");
  });

  it("reports a rejected signature as failed with its first line", () => {
    const phase = txPhase({
      ...base,
      writeError: { message: "User rejected the request.\n\nRequest Arguments:\n  from: 0x…" },
    });
    expect(phase).toEqual({ kind: "failed", reason: "User rejected the request." });
  });

  it("does not leave a failure without a reason", () => {
    const phase = txPhase({ ...base, writeError: { message: "" } });
    expect(phase.kind).toBe("failed");
    expect((phase as { reason: string }).reason.length).toBeGreaterThan(0);
  });

  it("reports a receipt that could not be fetched as failed, keeping the hash", () => {
    const phase = txPhase({ ...base, hash, receiptError: { message: "timed out" } });
    expect(phase).toEqual({ kind: "failed", reason: "timed out", hash });
  });
});
