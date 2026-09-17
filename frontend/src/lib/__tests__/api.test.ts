import { afterEach, describe, expect, it, vi } from "vitest";

import { api } from "../api";

afterEach(() => vi.unstubAllGlobals());

// An API serving several chains refuses a request that names none, so every call must name one.
describe("API client", () => {
  it("names the chain on every request", async () => {
    const urls: string[] = [];
    vi.stubGlobal("fetch", vi.fn(async (url: URL | string) => {
      urls.push(url.toString());
      return new Response(JSON.stringify({ items: [] }), { status: 200 });
    }));

    await api.oracleFeeds("solana-localnet");
    await api.proposal("anvil", "7");
    await api.commitmentsPage("anvil", 50, 100);
    await api.remoteActions("solana-localnet", "pending");

    expect(urls).toHaveLength(4);
    for (const u of urls) expect(new URL(u).searchParams.getAll("chain")).toHaveLength(1);
    expect(new URL(urls[0]!).searchParams.get("chain")).toBe("solana-localnet");
    expect(new URL(urls[2]!).searchParams.get("limit")).toBe("50");
    expect(new URL(urls[3]!).searchParams.get("status")).toBe("pending");
    expect(new URL(urls[3]!).pathname).toBe("/v1/governance/remote-actions");
  });

  it("reports an HTTP failure as an error, not as data", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 400 })));
    await expect(api.oracleNodes("anvil")).rejects.toThrow(/400/);
  });
});
