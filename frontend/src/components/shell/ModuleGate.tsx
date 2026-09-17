"use client";

import { ArrowRight, Layers } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { useChain, useServedChains, withChain } from "@/lib/chainContext";
import { hasModule, type Module } from "@/lib/chains";

const names: Record<Module, string> = {
  vault: "The vault",
  oracle: "The oracle network",
  governance: "The governor",
  "remote-governance": "Received governance",
  privacy: "The zk privacy gate",
};

// A module the chain does not have is explained, and the chains that do have it are offered. It is
// not an error, and not an empty list.
export function ModuleGate({ module, path, children }: { module: Module; path: string; children: ReactNode }) {
  const chain = useChain();
  const alternatives = useServedChains().filter((c) => hasModule(c, module));

  if (hasModule(chain, module)) return <>{children}</>;

  return (
    <div className="mx-auto flex max-w-md flex-col items-center gap-4 rounded-xl border border-dashed px-6 py-14 text-center" data-testid="module-unavailable">
      <div className="grid size-11 place-items-center rounded-full bg-muted">
        <Layers className="size-5 text-muted-foreground" aria-hidden="true" />
      </div>
      <div>
        <h2 className="font-display text-lg font-semibold">{names[module]} is not on {chain.label}</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {module === "governance" || module === "privacy"
            ? "It lives on Arbitrum. Solana receives governance decisions through Wormhole instead."
            : "It is served on another chain."}
        </p>
      </div>
      <div className="flex flex-wrap justify-center gap-2">
        {alternatives.map((c) => (
          <Button key={c.name} asChild variant="outline" size="sm">
            <Link href={withChain(path, c)}>
              Open on {c.label} <ArrowRight className="size-3.5" />
            </Link>
          </Button>
        ))}
      </div>
    </div>
  );
}
