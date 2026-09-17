"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { useChainHref } from "@/lib/chainContext";
import { cn } from "@/lib/utils";

export function SubNav({ links }: { links: { href: string; label: string }[] }) {
  const pathname = usePathname();
  const href = useChainHref();

  return (
    <nav aria-label="Section" className="inline-flex w-fit gap-1 rounded-lg border bg-muted/40 p-1 text-sm">
      {links.map((link) => {
        const active = pathname === link.href;
        return (
          <Link
            key={link.href}
            href={href(link.href)}
            aria-current={active ? "page" : undefined}
            className={cn(
              "rounded-md px-3 py-1.5 transition-colors duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              active ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
            )}
          >
            {link.label}
          </Link>
        );
      })}
    </nav>
  );
}
