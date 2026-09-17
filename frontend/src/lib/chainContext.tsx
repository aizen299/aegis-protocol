"use client";

import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useCallback, useMemo } from "react";

import { hasModule, servedChains, type Chain, type Module } from "./chains";
import { env } from "./env";

const served = servedChains(env.chains);
const defaultChain = served[0] as Chain;

export function useServedChains(): Chain[] {
  return served;
}

// The chain is the `chain` query parameter, so a link names the chain it is about. An absent or
// unserved name falls back to the default rather than guessing from anything else.
export function useChain(): Chain {
  const name = useSearchParams().get("chain");
  return useMemo(() => served.find((c) => c.name === name) ?? defaultChain, [name]);
}

export function withChain(href: string, chain: Chain): string {
  const [path, query = ""] = href.split("?");
  const params = new URLSearchParams(query);
  params.set("chain", chain.name);
  return `${path}?${params.toString()}`;
}

export function useChainHref(): (href: string) => string {
  const chain = useChain();
  return useCallback((href: string) => withChain(href, chain), [chain]);
}

// Detail pages are about one chain's ids, so switching chains returns to the module's root, or home
// when the new chain lacks the module.
export const moduleRoots: Record<Module, string> = {
  vault: "/",
  oracle: "/oracle",
  governance: "/governance",
  "remote-governance": "/governance/received",
  privacy: "/zk",
};

export function moduleOfPath(pathname: string): Module {
  if (pathname.startsWith("/oracle")) return "oracle";
  if (pathname.startsWith("/governance/received")) return "remote-governance";
  if (pathname.startsWith("/governance")) return "governance";
  if (pathname.startsWith("/zk")) return "privacy";
  return "vault";
}

export function switchTarget(pathname: string, to: Chain): string {
  const section = moduleOfPath(pathname);
  const root = hasModule(to, section) ? moduleRoots[section] : "/";
  const onRoot = pathname === root || (section === "oracle" && pathname === "/oracle/nodes");
  return withChain(onRoot ? pathname : root, to);
}

export function useSwitchChain(): (to: Chain) => void {
  const router = useRouter();
  const pathname = usePathname();
  return useCallback((to: Chain) => router.push(switchTarget(pathname, to)), [router, pathname]);
}
