"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";

export function SubNav({ links }: { links: { href: string; label: string }[] }) {
  const pathname = usePathname();

  return (
    <nav className="flex gap-3 text-xs">
      {links.map((link) => {
        const active = pathname === link.href;
        return (
          <Link
            key={link.href}
            href={link.href}
            aria-current={active ? "page" : undefined}
            className={
              active
                ? "rounded border border-edge bg-zinc-900 px-2 py-1 text-zinc-100"
                : "rounded border border-transparent px-2 py-1 text-zinc-500 hover:text-zinc-300"
            }
          >
            {link.label}
          </Link>
        );
      })}
    </nav>
  );
}
