"use client";

import type { ReactNode } from "react";

export function TxButton({
  children,
  disabled,
  pending,
  onClick,
}: {
  children: ReactNode;
  disabled?: boolean;
  pending?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled || pending}
      onClick={onClick}
      className="w-full rounded bg-zinc-100 px-4 py-2 text-sm font-medium text-zinc-900 transition disabled:cursor-not-allowed disabled:bg-zinc-700 disabled:text-zinc-400"
    >
      {pending ? "Confirming…" : children}
    </button>
  );
}

export function TxStatus({ error, hash }: { error?: Error | null; hash?: `0x${string}` }) {
  if (error) {
    return <p className="mt-2 break-words text-xs text-red-400">{shortenError(error.message)}</p>;
  }
  if (hash) {
    return <p className="mt-2 truncate font-mono text-xs text-emerald-400">{hash}</p>;
  }
  return null;
}

// Wallet errors carry the full RPC payload; only the first line is useful in the UI.
function shortenError(message: string): string {
  return message.split("\n")[0] ?? message;
}
