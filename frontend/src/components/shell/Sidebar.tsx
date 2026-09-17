"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { useChain, useChainHref } from "@/lib/chainContext";
import { cn } from "@/lib/utils";
import { ChainMark } from "./ChainMark";
import { isActive, navFor } from "./nav";

export function Brand() {
  return (
    <div className="flex items-center gap-2.5 px-2">
      <div className="grid size-8 place-items-center rounded-md bg-primary text-primary-foreground" aria-hidden="true">
        <svg viewBox="0 0 24 24" className="size-5">
          <path fill="currentColor" d="M12 2 4 5v6c0 5 3.4 9.4 8 11 4.6-1.6 8-6 8-11V5l-8-3Zm0 4.2 4 1.5v3.4c0 2.9-1.7 5.6-4 6.8V6.2Z" />
        </svg>
      </div>
      <div className="leading-tight">
        <p className="font-display text-[15px] font-semibold tracking-tight">Aegis</p>
        <p className="text-[11px] text-muted-foreground">Protocol</p>
      </div>
    </div>
  );
}

export function NavLinks({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname();
  const chain = useChain();
  const href = useChainHref();

  return (
    <nav aria-label="Main" className="flex flex-col gap-0.5">
      {navFor(chain).map((item) => {
        const active = isActive(item, pathname);
        const Icon = item.icon;
        return (
          <Link
            key={item.href}
            href={href(item.href)}
            onClick={onNavigate}
            aria-current={active ? "page" : undefined}
            className={cn(
              "group flex h-9 items-center gap-2.5 rounded-md px-2.5 text-sm transition-colors duration-150",
              "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              active
                ? "bg-accent text-foreground"
                : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
            )}
          >
            <Icon className={cn("size-4 shrink-0", active ? "text-primary" : "")} aria-hidden="true" />
            {item.label}
          </Link>
        );
      })}
    </nav>
  );
}

export function Sidebar() {
  const chain = useChain();
  return (
    <aside className="sticky top-0 hidden h-dvh w-60 shrink-0 flex-col gap-6 border-r bg-card/40 px-3 py-5 md:flex">
      <Brand />
      <NavLinks />
      <div className="mt-auto rounded-lg border bg-background/60 p-3">
        <div className="flex items-center gap-2 text-xs">
          <ChainMark family={chain.family} className="size-3.5" />
          <span className="font-medium">{chain.label}</span>
        </div>
        <p className="mt-1 text-[11px] leading-relaxed text-muted-foreground">
          {chain.family === "solana"
            ? "Governed from Arbitrum through Wormhole."
            : "Home of the governor and the zk gate."}
        </p>
      </div>
    </aside>
  );
}
