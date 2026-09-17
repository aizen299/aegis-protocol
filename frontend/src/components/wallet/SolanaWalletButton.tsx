"use client";

import { WalletReadyState } from "@solana/wallet-adapter-base";
import { useConnection, useWallet } from "@solana/wallet-adapter-react";
import { LAMPORTS_PER_SOL } from "@solana/web3.js";
import { ChevronDown, Copy, Droplets, LogOut, Wallet } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useMounted } from "@/hooks/useMounted";
import { useChain } from "@/lib/chainContext";
import { shortAddress } from "@/lib/format";
import { SOLANA_TEST_WALLET } from "@/lib/solana/testWallet";

export function SolanaWalletButton() {
  const { wallets, wallet, publicKey, select, disconnect, connecting } = useWallet();
  const { connection } = useConnection();
  const chain = useChain();
  const [open, setOpen] = useState(false);
  // The adapter restores a remembered wallet from storage, which the server cannot see.
  const mounted = useMounted();

  // The test wallet is listed only on solana-localnet, even when the provider holds it.
  const offered = wallets.filter(
    (w) => (w.adapter.name !== SOLANA_TEST_WALLET || chain.name === "solana-localnet") && w.readyState !== WalletReadyState.Unsupported,
  );
  const detected = offered.filter((w) => w.readyState === WalletReadyState.Installed || w.readyState === WalletReadyState.Loadable);

  if (mounted && publicKey && wallet) {
    const address = publicKey.toBase58();
    const isTest = wallet.adapter.name === SOLANA_TEST_WALLET;
    return (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" className="h-9 gap-2 px-2.5">
            {/* eslint-disable-next-line @next/next/no-img-element -- wallet icons are data URIs the wallet supplies */}
            <img src={wallet.adapter.icon} alt="" className="size-4 rounded-sm" />
            <span className="font-mono text-xs">{shortAddress(address)}</span>
            <ChevronDown className="size-3.5 text-muted-foreground" aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-56">
          <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">{wallet.adapter.name}</DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onSelect={() => navigator.clipboard.writeText(address).then(() => toast.success("Address copied"), () => undefined)}
          >
            <Copy className="size-4" /> Copy address
          </DropdownMenuItem>
          {isTest && chain.local ? (
            <DropdownMenuItem
              onSelect={async () => {
                const id = toast.loading("Requesting 2 SOL from the local validator…");
                try {
                  const sig = await connection.requestAirdrop(publicKey, 2 * LAMPORTS_PER_SOL);
                  await connection.confirmTransaction(sig, "confirmed");
                  toast.success("2 SOL airdropped for fees", { id });
                } catch (e) {
                  toast.error(`Airdrop failed: ${e instanceof Error ? e.message.split("\n")[0] : String(e)}`, { id });
                }
              }}
            >
              <Droplets className="size-4" /> Airdrop 2 SOL
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuItem onSelect={() => disconnect()}>
            <LogOut className="size-4" /> Disconnect
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button className="h-9 gap-2" disabled={connecting}>
          <Wallet className="size-4" aria-hidden="true" />
          {connecting ? "Connecting…" : "Connect"}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Connect a Solana wallet</DialogTitle>
          <DialogDescription>Deposits and withdrawals on {chain.label} are signed by your Solana wallet.</DialogDescription>
        </DialogHeader>
        {detected.length ? (
          <ul className="flex flex-col gap-1.5">
            {detected.map((w) => (
              <li key={w.adapter.name}>
                <button
                  type="button"
                  onClick={() => {
                    select(w.adapter.name);
                    setOpen(false);
                  }}
                  className="flex w-full items-center gap-3 rounded-md border px-3 py-2.5 text-left text-sm transition-colors hover:border-primary/40 hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  {/* eslint-disable-next-line @next/next/no-img-element -- wallet icons are data URIs the wallet supplies */}
                  <img src={w.adapter.icon} alt="" className="size-6 rounded" />
                  <span className="flex-1 font-medium">{w.adapter.name}</span>
                  {w.adapter.name === SOLANA_TEST_WALLET ? (
                    <span className="text-[11px] uppercase tracking-wide text-warning">local only</span>
                  ) : (
                    <span className="text-[11px] uppercase tracking-wide text-muted-foreground">detected</span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="rounded-md border border-dashed px-4 py-6 text-center text-sm text-muted-foreground">
            No Solana wallet was detected in this browser. Install Phantom, Solflare, or Backpack and reload.
          </p>
        )}
      </DialogContent>
    </Dialog>
  );
}
