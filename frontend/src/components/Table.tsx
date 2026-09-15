import type { ReactNode } from "react";

export function Table({ headers, children }: { headers: string[]; children: ReactNode }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-edge text-left">
            {headers.map((header) => (
              <th key={header} className="py-2 pr-4 font-medium text-zinc-500">
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
  return <tr className="border-b border-edge last:border-0">{children}</tr>;
}

export function Cell({ children, mono }: { children: ReactNode; mono?: boolean }) {
  return (
    <td className={`py-2 pr-4 text-zinc-300 ${mono ? "font-mono text-xs" : ""}`}>{children}</td>
  );
}
