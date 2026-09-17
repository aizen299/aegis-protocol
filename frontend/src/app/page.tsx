"use client";

import { VaultDashboard } from "@/components/VaultDashboard";
import { SolanaVaultDashboard } from "@/components/vault/SolanaVaultDashboard";
import { useChain } from "@/lib/chainContext";

export default function Home() {
  return useChain().vm === "svm" ? <SolanaVaultDashboard /> : <VaultDashboard />;
}
