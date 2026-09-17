import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

import { chainById, chainByName, chains, servedChains } from "../chains";

const goSource = readFileSync(resolve(process.cwd(), "../backend/pkg/types/chain.go"), "utf8");

// Resolves the Go constants the registry is written in, including the 1<<62 base.
function goChains(): { name: string; id: string; vm: string }[] {
  const consts = new Map<string, bigint>();
  for (const m of goSource.matchAll(/(\w+)\s+int64 = ([^\n]+)/g)) {
    const expr = m[2]!.trim();
    const shift = expr.match(/^1 << (\d+)$/);
    const sum = expr.match(/^(\w+) \+ (\d+)$/);
    consts.set(m[1]!, shift ? 1n << BigInt(shift[1]!) : sum ? consts.get(sum[1]!)! + BigInt(sum[2]!) : BigInt(expr));
  }
  const block = goSource.slice(goSource.indexOf("var knownChains"));
  return [...block.matchAll(/\{(\w+), "([\w-]+)", VM(EVM|SVM)\}/g)].map((m) => ({
    id: consts.get(m[1]!)!.toString(),
    name: m[2]!,
    vm: m[3]!.toLowerCase(),
  }));
}

describe("chain registry", () => {
  it("mirrors backend/pkg/types/chain.go exactly", () => {
    const go = goChains();
    expect(go.length).toBeGreaterThan(0);
    expect(chains.map(({ name, id, vm }) => ({ name, id, vm }))).toEqual(go);
  });

  // The reason ids are strings: parsed as JSON numbers, the three Solana clusters are one float.
  it("keeps Solana ids apart where a JSON number would merge them", () => {
    const parsed = ["solana-mainnet", "solana-devnet", "solana-localnet"].map(
      (n) => JSON.parse(chainByName(n)!.id) as number,
    );
    expect(new Set(parsed).size).toBe(1);
    expect(chainById("4611686018427387907")?.name).toBe("solana-localnet");
    expect(chainById(4611686018427387906n)?.name).toBe("solana-devnet");
  });

  it("serves the configured chains in order and refuses unknown names", () => {
    expect(servedChains("solana-localnet, anvil").map((c) => c.name)).toEqual(["solana-localnet", "anvil"]);
    expect(servedChains(undefined).map((c) => c.name)).toEqual(["anvil"]);
    expect(() => servedChains("anvil,solana")).toThrow(/solana/);
    expect(() => servedChains(" , ")).toThrow();
  });

  it("links to no explorer for local chains", () => {
    for (const c of chains) expect(Boolean(c.explorer)).toBe(!c.local);
    expect(chainByName("solana-devnet")!.explorer!.tx("abc")).toBe("https://explorer.solana.com/tx/abc?cluster=devnet");
  });
});
