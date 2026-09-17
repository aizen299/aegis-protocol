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
      <p className="rounded border border-warning/40 bg-warning/10 px-3 py-1 text-xs text-warning">
        Test wallet — Anvil development account, local chain only
      </p>
    );
  }

  return (
    <button
      type="button"
      onClick={() => connect({ connector: testConnector })}
      className="rounded border border-warning/40 px-3 py-1 text-xs text-warning hover:bg-warning/10"
    >
      Connect test wallet (Anvil)
    </button>
  );
}
