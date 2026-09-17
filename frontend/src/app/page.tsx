"use client";

import { VaultDashboard } from "@/components/VaultDashboard";
import { SolanaVaultPending } from "@/components/vault/SolanaVaultPending";
import { useChain } from "@/lib/chainContext";

export default function Home() {
  return useChain().vm === "svm" ? <SolanaVaultPending /> : <VaultDashboard />;
}
