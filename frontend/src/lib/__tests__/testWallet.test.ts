import { describe, expect, it } from "vitest";

import { ANVIL_ACCOUNT, solanaTestWalletEnabled, testWalletAccount } from "../testWallet";

describe("testWalletAccount", () => {
  it("is enabled only with the flag on Anvil's chain", () => {
    expect(testWalletAccount({ flag: "anvil", chainId: 31337 })).toBe(ANVIL_ACCOUNT);
  });

  // The failure that matters: public development keys offered against a real network.
  it("is refused on any other chain even with the flag set", () => {
    for (const chainId of [1, 42161, 421614, 11155111]) {
      expect(testWalletAccount({ flag: "anvil", chainId })).toBeUndefined();
    }
  });

  it("is off unless explicitly requested, including on Anvil", () => {
    for (const flag of [undefined, "", "true", "1", "ANVIL"]) {
      expect(testWalletAccount({ flag, chainId: 31337 })).toBeUndefined();
    }
  });
});

describe("solanaTestWalletEnabled", () => {
  it("is offered only with the flag and only on solana-localnet", () => {
    expect(solanaTestWalletEnabled({ flag: "anvil,solana-localnet", chain: "solana-localnet" })).toBe(true);
    for (const chain of ["solana-devnet", "solana-mainnet", "anvil"]) {
      expect(solanaTestWalletEnabled({ flag: "solana-localnet", chain })).toBe(false);
    }
    for (const flag of [undefined, "", "anvil", "solana", "SOLANA-LOCALNET"]) {
      expect(solanaTestWalletEnabled({ flag, chain: "solana-localnet" })).toBe(false);
    }
  });

  it("does not let the Solana flag enable the Anvil wallet", () => {
    expect(testWalletAccount({ flag: "solana-localnet", chainId: 31337 })).toBeUndefined();
    expect(testWalletAccount({ flag: "solana-localnet, anvil", chainId: 31337 })).toBe(ANVIL_ACCOUNT);
  });
});
