"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

const links = [
  { href: "/", label: "Vault" },
  { href: "/oracle", label: "Oracle" },
  { href: "/oracle/nodes", label: "Nodes" },
  { href: "/governance", label: "Governance" },
];

export function Nav() {
  const pathname = usePathname();

  return (
    <nav className="flex gap-4 border-b border-edge pb-3 text-sm">
      {links.map((link) => {
        const active = link.href === "/" ? pathname === "/" : pathname.startsWith(link.href);
        return (
          <Link
            key={link.href}
            href={link.href}
            aria-current={active ? "page" : undefined}
            className={active ? "text-zinc-100" : "text-zinc-500 hover:text-zinc-300"}
          >
            {link.label}
          </Link>
        );
      })}
    </nav>
  );
}
