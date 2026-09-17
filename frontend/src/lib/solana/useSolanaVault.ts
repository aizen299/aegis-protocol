"use client";

import { useConnection, useWallet } from "@solana/wallet-adapter-react";
import { PublicKey } from "@solana/web3.js";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";

import { useMounted } from "@/hooks/useMounted";
import { useChain } from "@/lib/chainContext";
import { env } from "@/lib/env";
import { associatedTokenAddress, decodePositionShares, decodeVault, positionAddress, vaultAddress, vaultTokensAddress } from "./vault";

// SPL layouts: a token account's amount sits at offset 64, a mint's decimals at 44.
const u64At = (data: Uint8Array, offset: number) => {
  let n = 0n;
  for (let i = 7; i >= 0; i--) n = (n << 8n) | BigInt(data[offset + i]!);
  return n;
};

export function parseMint(value: string): PublicKey | null {
  try {
    return value ? new PublicKey(value) : null;
  } catch {
    return null;
  }
}

export function useSolanaVault() {
  const chain = useChain().name;
  const { connection } = useConnection();
  const { publicKey: connected } = useWallet();
  // Null until mounted: a remembered wallet is restored from storage the server cannot see.
  const publicKey = useMounted() ? connected : null;
  const mint = useMemo(() => parseMint(env.solanaVaultMint), []);
  const vault = mint ? vaultAddress(mint) : null;

  const state = useQuery({
    queryKey: [chain, "solana-vault", mint?.toBase58()],
    enabled: Boolean(mint),
    staleTime: 5_000,
    retry: 1,
    queryFn: async () => {
      const [vaultInfo, tokensInfo, mintInfo] = await connection.getMultipleAccountsInfo([vault!, vaultTokensAddress(vault!), mint!], "confirmed");
      if (!vaultInfo) throw new Error(`no vault exists for mint ${mint!.toBase58()}`);
      if (!tokensInfo || !mintInfo) throw new Error("the vault's token account or mint could not be read");
      return { ...decodeVault(vaultInfo.data), totalAssets: u64At(tokensInfo.data, 64), decimals: mintInfo.data[44]! };
    },
  });

  const position = useQuery({
    queryKey: [chain, "solana-vault", mint?.toBase58(), "position", publicKey?.toBase58()],
    enabled: Boolean(mint && publicKey),
    staleTime: 5_000,
    retry: 1,
    queryFn: async () => {
      const [positionInfo, walletTokens] = await connection.getMultipleAccountsInfo(
        [positionAddress(vault!, publicKey!), associatedTokenAddress(publicKey!, mint!)],
        "confirmed",
      );
      return {
        // An absent position account is a real zero: this owner has never deposited.
        shares: positionInfo ? decodePositionShares(positionInfo.data) : 0n,
        walletBalance: walletTokens ? u64At(walletTokens.data, 64) : null,
      };
    },
  });

  return { mint, vault, state, position, owner: publicKey };
}
