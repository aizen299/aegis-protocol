"use client";

import { AlertTriangle, Inbox } from "lucide-react";
import type { ReactNode } from "react";

import { Skeleton } from "@/components/ui/skeleton";

// The list-level counterpart to ReadValue: loading, failed, and genuinely empty stay three separate
// renderings. An unreachable API must never look like a protocol with nothing in it.
export function Async<T>({
  query,
  empty,
  children,
}: {
  query: { isPending: boolean; isError: boolean; error?: { message?: string } | null; data?: T };
  empty: string;
  children: (data: T) => ReactNode;
}) {
  if (query.isPending) {
    return (
      <div className="flex flex-col gap-2" aria-busy="true" data-testid="async-loading">
        <span className="sr-only">loading…</span>
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-9 w-full" />
        ))}
      </div>
    );
  }

  if (query.isError || query.data === undefined) {
    return (
      <div
        className="flex items-start gap-3 rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm"
        role="alert"
        data-testid="async-failed"
      >
        <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden="true" />
        <p>
          Could not load this from the API. This is a failure to read, not an absence of data.
          {query.error?.message ? ` (${query.error.message})` : ""}
        </p>
      </div>
    );
  }

  if (Array.isArray(query.data) && query.data.length === 0) {
    return (
      <div className="flex flex-col items-center gap-2 py-8 text-center" data-testid="async-empty">
        <Inbox className="size-5 text-muted-foreground" aria-hidden="true" />
        <p className="text-sm text-muted-foreground">{empty}</p>
      </div>
    );
  }

  return <>{children(query.data)}</>;
}
