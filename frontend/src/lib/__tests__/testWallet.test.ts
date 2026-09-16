import { describe, expect, it } from "vitest";

import { ANVIL_ACCOUNT, testWalletAccount } from "../testWallet";

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
