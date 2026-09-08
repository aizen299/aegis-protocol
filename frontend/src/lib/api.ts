import { env } from "./env";

export type VaultPosition = {
  chainId: number;
  user: string;
  shares: string;
  depositedTotal: string;
  withdrawnTotal: string;
  lastDepositAt?: string;
};

// Indexed history comes from the backend, not the chain: the UI must not re-implement indexing.
export async function fetchPosition(address: string): Promise<VaultPosition | null> {
  const res = await fetch(`${env.apiUrl}/v1/vault/positions/${address}`, { cache: "no-store" });
  if (!res.ok) return null;
  return (await res.json()) as VaultPosition;
}

export async function fetchTvl(vaultAddress: string): Promise<string | null> {
  const res = await fetch(`${env.apiUrl}/v1/vault/${vaultAddress}/tvl`, { cache: "no-store" });
  if (!res.ok) return null;
  const body = (await res.json()) as { tvl: string };
  return body.tvl;
}
