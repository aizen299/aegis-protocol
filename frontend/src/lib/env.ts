function required(name: string, value: string | undefined): string {
  if (!value) throw new Error(`${name} is not set`);
  return value;
}

export const env = {
  chainId: Number(process.env.NEXT_PUBLIC_CHAIN_ID ?? 31337),
  rpcUrl: process.env.NEXT_PUBLIC_RPC_URL ?? "http://127.0.0.1:8545",
  apiUrl: process.env.NEXT_PUBLIC_API_URL ?? "http://127.0.0.1:8090",
  vaultAddress: (process.env.NEXT_PUBLIC_VAULT_ADDRESS ?? "") as `0x${string}`,
  assetAddress: (process.env.NEXT_PUBLIC_ASSET_ADDRESS ?? "") as `0x${string}`,
  walletConnectProjectId: process.env.NEXT_PUBLIC_WALLETCONNECT_PROJECT_ID ?? "",
};

export function requireVaultAddress(): `0x${string}` {
  return required("NEXT_PUBLIC_VAULT_ADDRESS", env.vaultAddress) as `0x${string}`;
}
