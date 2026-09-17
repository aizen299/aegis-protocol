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
      <p className="hidden rounded-md border border-warning/40 bg-warning/10 px-2.5 py-1.5 text-xs text-warning lg:block">
        Test wallet (Anvil)
      </p>
    );
  }

  return (
    <button
      type="button"
      onClick={() => connect({ connector: testConnector })}
      className="h-9 rounded-md border border-warning/40 px-3 text-xs text-warning transition-colors hover:bg-warning/10"
    >
      Connect test wallet (Anvil)
    </button>
  );
}
