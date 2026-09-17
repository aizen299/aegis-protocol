"use client";

import { ConnectButton } from "@rainbow-me/rainbowkit";
import { AlertTriangle, Wallet } from "lucide-react";

import { Button } from "@/components/ui/button";

// RainbowKit's flows behind a button in this app's own style, compact enough for a phone header.
export function EvmWalletButton() {
  return (
    <ConnectButton.Custom>
      {({ account, chain, mounted, openAccountModal, openChainModal, openConnectModal }) => {
        if (!mounted || !account || !chain) {
          return (
            <Button className="h-9 gap-2" onClick={openConnectModal} aria-hidden={!mounted} disabled={!mounted}>
              <Wallet className="size-4" aria-hidden="true" />
              Connect
            </Button>
          );
        }
        if (chain.unsupported) {
          return (
            <Button variant="destructive" className="h-9 gap-2" onClick={openChainModal}>
              <AlertTriangle className="size-4" aria-hidden="true" />
              Wrong network
            </Button>
          );
        }
        return (
          <Button variant="outline" className="h-9 gap-2 px-2.5" onClick={openAccountModal}>
            <span className="size-2 rounded-full bg-success" aria-hidden="true" />
            <span className="font-mono text-xs">{account.displayName}</span>
          </Button>
        );
      }}
    </ConnectButton.Custom>
  );
}
