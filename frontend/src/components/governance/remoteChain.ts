import { chains, type Chain } from "@/lib/chains";

// The API's numeric chain ids are exact for EVM chains and not for Solana, whose ids all parse to one
// float. A served chain is matched by that float, preferring the served list, so the label is right for
// any deployment serving one Solana cluster.
export function chainForNumericId(id: number, served: readonly Chain[]): Chain | undefined {
  return served.find((c) => Number(c.id) === id) ?? chains.find((c) => Number(c.id) === id);
}
