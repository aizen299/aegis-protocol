import { describe, expect, it } from "vitest";

import { moduleOfPath, switchTarget, withChain } from "../chainContext";
import { chainByName } from "../chains";

const anvil = chainByName("anvil")!;
const solana = chainByName("solana-localnet")!;

describe("chain in links", () => {
  it("sets the chain and keeps other parameters", () => {
    expect(withChain("/oracle", solana)).toBe("/oracle?chain=solana-localnet");
    expect(withChain("/x?status=pending&chain=anvil", solana)).toBe("/x?status=pending&chain=solana-localnet");
  });

  it("maps paths to modules", () => {
    expect(moduleOfPath("/")).toBe("vault");
    expect(moduleOfPath("/oracle/rounds/4")).toBe("oracle");
    expect(moduleOfPath("/governance/12")).toBe("governance");
    expect(moduleOfPath("/governance/received")).toBe("remote-governance");
    expect(moduleOfPath("/zk/commitments")).toBe("privacy");
  });
});

// A detail page is about one chain's ids: a round number on Anvil means nothing on Solana.
describe("switching chains", () => {
  it("stays on a module root the other chain has", () => {
    expect(switchTarget("/oracle", solana)).toBe("/oracle?chain=solana-localnet");
    expect(switchTarget("/oracle/nodes", solana)).toBe("/oracle/nodes?chain=solana-localnet");
  });

  it("leaves a detail page for its module root", () => {
    expect(switchTarget("/oracle/rounds/7", solana)).toBe("/oracle?chain=solana-localnet");
  });

  it("goes home when the other chain lacks the module", () => {
    expect(switchTarget("/governance/3", solana)).toBe("/?chain=solana-localnet");
    expect(switchTarget("/zk/actions", solana)).toBe("/?chain=solana-localnet");
    expect(switchTarget("/governance/received", anvil)).toBe("/?chain=anvil");
  });
});
