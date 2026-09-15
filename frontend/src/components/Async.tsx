"use client";

import type { ReactNode } from "react";

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
      <p className="text-sm text-zinc-500" aria-busy="true" data-testid="async-loading">
        loading…
      </p>
    );
  }

  if (query.isError || query.data === undefined) {
    return (
      <p
        className="rounded border border-red-900/60 bg-red-950/40 px-4 py-2 text-sm text-red-200"
        role="alert"
        data-testid="async-failed"
      >
        Could not load this from the API. This is a failure to read, not an absence of data.
        {query.error?.message ? ` (${query.error.message})` : ""}
      </p>
    );
  }

  if (Array.isArray(query.data) && query.data.length === 0) {
    return (
      <p className="text-sm text-zinc-500" data-testid="async-empty">
        {empty}
      </p>
    );
  }

  return <>{children(query.data)}</>;
}
