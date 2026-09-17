import { ArrowDownToLine, EyeOff, Landmark, Radio, Server, Vote, type LucideIcon } from "lucide-react";

import { hasModule, type Chain, type Module } from "@/lib/chains";

export type NavItem = { href: string; label: string; icon: LucideIcon; module: Module; exact?: boolean };

const items: NavItem[] = [
  { href: "/", label: "Vault", icon: Landmark, module: "vault", exact: true },
  { href: "/oracle", label: "Oracle feeds", icon: Radio, module: "oracle", exact: true },
  { href: "/oracle/nodes", label: "Oracle nodes", icon: Server, module: "oracle" },
  { href: "/governance", label: "Governance", icon: Vote, module: "governance" },
  { href: "/governance/received", label: "Received governance", icon: ArrowDownToLine, module: "remote-governance" },
  { href: "/zk", label: "Privacy", icon: EyeOff, module: "privacy" },
];

// Only what the chain has: a module a chain lacks is not offered, rather than offered and empty.
export function navFor(chain: Chain): NavItem[] {
  return items.filter((item) => hasModule(chain, item.module));
}

export function isActive(item: NavItem, pathname: string): boolean {
  if (item.exact) return pathname === item.href || (item.href === "/oracle" && /^\/oracle\/(feeds|rounds)/.test(pathname));
  if (item.href === "/governance") return pathname.startsWith("/governance") && !pathname.startsWith("/governance/received");
  return pathname.startsWith(item.href);
}
