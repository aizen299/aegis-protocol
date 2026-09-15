import { describe, expect, it } from "vitest";

import { resolve } from "../readState";

const settled = { isPending: false, isError: false, error: null };

describe("resolve", () => {
  it("reports a pending query as loading, not as an absent value", () => {
    expect(resolve({ isPending: true, isError: false, error: null }, () => undefined).kind).toBe(
      "loading",
    );
  });

  // A refetch after a failure is pending, not failed. Reporting it as failed flickers an error
  // banner on every poll.
  it("prefers loading over failed while a query is pending", () => {
    const state = resolve({ isPending: true, isError: true, error: { message: "boom" } }, () => "1");
    expect(state.kind).toBe("loading");
  });

  it("carries the failure reason through", () => {
    const state = resolve({ isPending: false, isError: true, error: { message: "no rpc" } }, () => undefined);
    expect(state).toEqual({ kind: "failed", reason: "no rpc" });
  });

  // The case that made this module necessary: the query itself succeeded, but the value came back
  // undefined. Treating that as an empty vault is what hid a total read failure.
  it("treats a successful query with no value as failed, not as empty", () => {
    expect(resolve(settled, () => undefined).kind).toBe("failed");
  });

  it("passes a formatted value through", () => {
    expect(resolve(settled, () => "250 tUSD")).toEqual({ kind: "value", text: "250 tUSD" });
  });

  it("substitutes a reason when the error carries none", () => {
    const state = resolve({ isPending: false, isError: true, error: { message: "  " } }, () => "x");
    expect(state).toMatchObject({ kind: "failed" });
    expect((state as { reason: string }).reason.length).toBeGreaterThan(0);
  });
});
