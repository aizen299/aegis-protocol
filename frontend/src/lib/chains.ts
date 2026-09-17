// Mirrors backend/pkg/types/chain.go; a test compares the two.
//
// A chain is identified by its registry name everywhere in the UI. Solana's internal ids are above
// 2^53, so JSON.parse turns all three into the same float: a numeric chainId from the API is never
// used to tell chains apart. docs/v2.0-solana-plan.md §22.

export type ChainName =
  | "arbitrum-one"
  | "arbitrum-sepolia"
  | "anvil"
  | "solana-mainnet"
  | "solana-devnet"
  | "solana-localnet";

export type Vm = "evm" | "svm";
export type Family = "arbitrum" | "solana";
export type Module = "vault" | "oracle" | "governance" | "remote-governance" | "privacy";

export type Chain = {
  name: ChainName;
  // Decimal string: exact where a number would not be.
  id: string;
  vm: Vm;
  family: Family;
  label: string;
  local: boolean;
  modules: readonly Module[];
  explorer?: { address: (a: string) => string; tx: (h: string) => string };
};

const evmModules = ["vault", "oracle", "governance", "privacy"] as const;
const svmModules = ["vault", "oracle", "remote-governance"] as const;

const arbiscan = (base: string) => ({
  address: (a: string) => `${base}/address/${a}`,
  tx: (h: string) => `${base}/tx/${h}`,
});

const solanaExplorer = (cluster?: string) => {
  const q = cluster ? `?cluster=${cluster}` : "";
  return {
    address: (a: string) => `https://explorer.solana.com/address/${a}${q}`,
    tx: (h: string) => `https://explorer.solana.com/tx/${h}${q}`,
  };
};

export const chains: readonly Chain[] = [
  { name: "arbitrum-one", id: "42161", vm: "evm", family: "arbitrum", label: "Arbitrum One", local: false, modules: evmModules, explorer: arbiscan("https://arbiscan.io") },
  { name: "arbitrum-sepolia", id: "421614", vm: "evm", family: "arbitrum", label: "Arbitrum Sepolia", local: false, modules: evmModules, explorer: arbiscan("https://sepolia.arbiscan.io") },
  { name: "anvil", id: "31337", vm: "evm", family: "arbitrum", label: "Anvil", local: true, modules: evmModules },
  { name: "solana-mainnet", id: "4611686018427387905", vm: "svm", family: "solana", label: "Solana", local: false, modules: svmModules, explorer: solanaExplorer() },
  { name: "solana-devnet", id: "4611686018427387906", vm: "svm", family: "solana", label: "Solana Devnet", local: false, modules: svmModules, explorer: solanaExplorer("devnet") },
  { name: "solana-localnet", id: "4611686018427387907", vm: "svm", family: "solana", label: "Solana Localnet", local: true, modules: svmModules },
];

export function chainByName(name: string | null | undefined): Chain | undefined {
  return chains.find((c) => c.name === name);
}

export function chainById(id: string | bigint): Chain | undefined {
  const key = id.toString();
  return chains.find((c) => c.id === key);
}

// The chains this deployment serves, in order; the first is the default. Unknown names are refused
// at startup rather than dropped, mirroring API_CHAINS.
export function servedChains(configured: string | undefined): Chain[] {
  const names = (configured ?? "anvil").split(",").map((s) => s.trim()).filter(Boolean);
  if (names.length === 0) throw new Error("NEXT_PUBLIC_CHAINS names no chain");
  return names.map((n) => {
    const c = chainByName(n);
    if (!c) throw new Error(`NEXT_PUBLIC_CHAINS names ${n}, which is not a known chain`);
    return c;
  });
}

export function hasModule(chain: Chain, module: Module): boolean {
  return chain.modules.includes(module);
}
