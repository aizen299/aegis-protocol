"use client";

import { Check, ChevronsUpDown } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useChain, useServedChains, useSwitchChain } from "@/lib/chainContext";
import { ChainMark } from "./ChainMark";

export function ChainSwitcher() {
  const chain = useChain();
  const served = useServedChains();
  const switchChain = useSwitchChain();

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" className="h-9 min-w-0 gap-2 px-2.5 font-medium sm:px-3" aria-label={`Chain: ${chain.label}. Switch chain`}>
          <ChainMark family={chain.family} />
          <span className="hidden sm:inline">{chain.label}</span>
          <ChevronsUpDown className="hidden size-3.5 text-muted-foreground sm:block" aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">Chain</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {served.map((c) => (
          <DropdownMenuItem key={c.name} onSelect={() => switchChain(c)} className="gap-2">
            <ChainMark family={c.family} />
            <span className="flex-1">{c.label}</span>
            {c.local ? <span className="text-[11px] uppercase tracking-wide text-muted-foreground">local</span> : null}
            {c.name === chain.name ? <Check className="size-4 text-primary" aria-label="selected" /> : null}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
