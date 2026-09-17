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
      className="w-full rounded bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition disabled:cursor-not-allowed disabled:bg-muted disabled:text-muted-foreground"
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
        <p className="mt-2 text-xs text-muted-foreground" data-testid="tx-signing">
          Waiting for your wallet…
        </p>
      );
    case "pending":
      return (
        <p className="mt-2 text-xs text-muted-foreground" data-testid="tx-pending">
          Submitted, not yet included: <Hash value={phase.hash} />
        </p>
      );
    case "confirmed":
      return (
        <p className="mt-2 text-xs text-success" data-testid="tx-confirmed">
          Confirmed: <Hash value={phase.hash} />
        </p>
      );
    case "reverted":
      return (
        <p className="mt-2 text-xs text-destructive" role="alert" data-testid="tx-reverted">
          Included but reverted — nothing changed: <Hash value={phase.hash} />
        </p>
      );
    case "failed":
      return (
        <p className="mt-2 break-words text-xs text-destructive" role="alert" data-testid="tx-failed">
          {phase.reason}
        </p>
      );
  }
}

function Hash({ value }: { value: string }) {
  return <span className="font-mono">{value}</span>;
}
