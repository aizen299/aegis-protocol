"use client";

import { Check, Copy, ExternalLink } from "lucide-react";
import { useState } from "react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useChain } from "@/lib/chainContext";
import type { Chain } from "@/lib/chains";
import { shortAddress } from "@/lib/format";
import { cn } from "@/lib/utils";

// An address or transaction in its chain's form: short, copyable in full, and linked to an explorer only
// where one exists for the chain. A local chain's address links nowhere.
export function ChainValue({
  value,
  kind,
  chain: override,
  full,
  className,
}: {
  value: string;
  kind: "address" | "tx";
  chain?: Chain;
  full?: boolean;
  className?: string;
}) {
  const current = useChain();
  const chain = override ?? current;
  const [copied, setCopied] = useState(false);
  const href = chain.explorer ? chain.explorer[kind](value) : undefined;

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    } catch {
      // Copy is a convenience; the full value stays in the tooltip.
    }
  }

  return (
    <span className={cn("inline-flex min-w-0 items-center gap-1 font-mono text-xs", className)}>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="truncate">{full ? value : shortAddress(value)}</span>
        </TooltipTrigger>
        <TooltipContent className="max-w-[90vw] break-all font-mono text-xs">{value}</TooltipContent>
      </Tooltip>
      <button
        type="button"
        onClick={copy}
        aria-label={copied ? "Copied" : `Copy ${kind === "tx" ? "transaction" : "address"}`}
        className="grid size-6 shrink-0 place-items-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        {copied ? <Check className="size-3.5 text-success" /> : <Copy className="size-3.5" />}
      </button>
      {href ? (
        <a
          href={href}
          target="_blank"
          rel="noreferrer"
          aria-label={`Open in ${chain.label} explorer`}
          className="grid size-6 shrink-0 place-items-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <ExternalLink className="size-3.5" />
        </a>
      ) : null}
    </span>
  );
}
