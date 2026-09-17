"use client";

import { ConnectionProvider, WalletProvider } from "@solana/wallet-adapter-react";
import { useMemo, type ReactNode } from "react";

import { useServedChains } from "@/lib/chainContext";
import { env } from "@/lib/env";
import { solanaTestWalletEnabled } from "@/lib/testWallet";
import { LocalnetTestWalletAdapter } from "./testWallet";

// Browser wallets (Phantom, Solflare, Backpack) announce themselves through the Wallet Standard, so no
// adapter is listed for them. Only the localnet test wallet is added, and only where it is allowed.
export function SolanaProviders({ children }: { children: ReactNode }) {
  const served = useServedChains();
  const wallets = useMemo(
    () =>
      served.some((c) => solanaTestWalletEnabled({ flag: env.testWallet, chain: c.name }))
        ? [new LocalnetTestWalletAdapter()]
        : [],
    [served],
  );

  return (
    <ConnectionProvider endpoint={env.solanaRpcUrl} config={{ commitment: "confirmed" }}>
      <WalletProvider wallets={wallets} autoConnect>
        {children}
      </WalletProvider>
    </ConnectionProvider>
  );
}
