import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export function Table({ headers, children }: { headers: string[]; children: ReactNode }) {
  return (
    <div className="-mx-1 overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-left">
            {headers.map((header) => (
              <th key={header} className="whitespace-nowrap px-1 py-2.5 pr-4 text-xs font-medium uppercase tracking-wider text-muted-foreground">
                {header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

export function Row({ children }: { children: ReactNode }) {
  return <tr className="border-b transition-colors last:border-0 hover:bg-accent/40">{children}</tr>;
}

export function Cell({ children, mono }: { children: ReactNode; mono?: boolean }) {
  return <td className={cn("px-1 py-2.5 pr-4 align-middle", mono && "tabular font-mono text-xs")}>{children}</td>;
}
