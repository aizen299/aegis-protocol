"use client";

import { useAccount, useConnect } from "wagmi";

import { testWalletEnabled } from "@/lib/wagmi";

// Rendered only when the guarded test wallet is enabled. It announces itself: a connected wallet
// that is secretly a public development key should never be mistaken for the user's own.
export function TestWalletButton() {
  const { connectors, connect } = useConnect();
  const { isConnected, connector } = useAccount();

  if (!testWalletEnabled) return null;

  const testConnector = connectors.find((c) => c.type === "mock");
  if (!testConnector) return null;

  if (isConnected && connector?.type === "mock") {
    return (
      <p className="rounded border border-amber-900/60 bg-amber-950/40 px-3 py-1 text-xs text-amber-200">
        Test wallet — Anvil development account, local chain only
      </p>
    );
  }

  return (
    <button
      type="button"
      onClick={() => connect({ connector: testConnector })}
      className="rounded border border-amber-900/60 px-3 py-1 text-xs text-amber-200 hover:bg-amber-950/40"
    >
      Connect test wallet (Anvil)
    </button>
  );
}
