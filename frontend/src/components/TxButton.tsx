"use client";

import type { ReactNode } from "react";

import type { TxPhase } from "@/lib/tx";

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

export function TxStatus({ phase }: { phase: TxPhase }) {
  switch (phase.kind) {
    case "idle":
      return null;
    case "signing":
      return (
        <p className="mt-2 text-xs text-zinc-400" data-testid="tx-signing">
          Waiting for your wallet…
        </p>
      );
    case "pending":
      return (
        <p className="mt-2 text-xs text-zinc-400" data-testid="tx-pending">
          Submitted, not yet included: <Hash value={phase.hash} />
        </p>
      );
    case "confirmed":
      return (
        <p className="mt-2 text-xs text-emerald-400" data-testid="tx-confirmed">
          Confirmed: <Hash value={phase.hash} />
        </p>
      );
    case "reverted":
      return (
        <p className="mt-2 text-xs text-red-400" role="alert" data-testid="tx-reverted">
          Included but reverted — nothing changed: <Hash value={phase.hash} />
        </p>
      );
    case "failed":
      return (
        <p className="mt-2 break-words text-xs text-red-400" role="alert" data-testid="tx-failed">
          {phase.reason}
        </p>
      );
  }
}

function Hash({ value }: { value: string }) {
  return <span className="font-mono">{value}</span>;
}
