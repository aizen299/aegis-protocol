import type { ReactNode } from "react";

import type { ReadState } from "@/lib/readState";
import { ReadValue } from "./ReadValue";

export function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="rounded-lg border border-edge bg-panel p-5">
      <h2 className="mb-4 text-sm font-medium uppercase tracking-wide text-zinc-400">{title}</h2>
      {children}
    </section>
  );
}

export function Stat({ label, state }: { label: string; state: ReadState }) {
  return (
    <div className="flex items-baseline justify-between border-b border-edge py-2 last:border-0">
      <span className="text-sm text-zinc-500">{label}</span>
      <ReadValue state={state} />
    </div>
  );
}
