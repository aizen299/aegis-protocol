// A wallet that needs no browser extension, for verifying write paths against a local chain.
//
// It sends through eth_sendTransaction, which only a node holding the account's key will honour —
// Anvil does, for its well-known development accounts. Those keys are public, so a UI that offered
// this against any other chain would be offering to sign with a key everyone has.
//
// Two independent conditions, both required: the flag is set, and the configured chain is Anvil's.
// Either alone is refused.

const ANVIL_CHAIN_ID = 31337;

// Anvil's first default account. Public knowledge, worthless off a local chain.
export const ANVIL_ACCOUNT = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266" as const;

export function testWalletAccount(input: {
  flag: string | undefined;
  chainId: number;
}): `0x${string}` | undefined {
  if (!flagNames(input.flag).includes("anvil")) return undefined;
  if (input.chainId !== ANVIL_CHAIN_ID) return undefined;
  return ANVIL_ACCOUNT;
}

function flagNames(flag: string | undefined): string[] {
  return (flag ?? "").split(",").map((s) => s.trim());
}

// The Solana test wallet holds a keypair generated in this browser, which no one else has, and is
// offered only on solana-localnet: its airdrops and its funds exist nowhere else.
export function solanaTestWalletEnabled(input: { flag: string | undefined; chain: string }): boolean {
  return flagNames(input.flag).includes("solana-localnet") && input.chain === "solana-localnet";
}
