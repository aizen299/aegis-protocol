"use client";

import { Loader2 } from "lucide-react";
import type { ReactNode } from "react";

import { ChainValue } from "@/components/common/Address";
import { Button } from "@/components/ui/button";
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
    <Button type="button" className="w-full" disabled={disabled || pending} onClick={onClick}>
      {pending ? (
        <>
          <Loader2 className="size-4 animate-spin" aria-hidden="true" /> Confirming…
        </>
      ) : (
        children
      )}
    </Button>
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
        <p className="mt-2 flex flex-wrap items-center gap-1 text-xs text-muted-foreground" data-testid="tx-pending">
          Submitted, not yet included: <ChainValue kind="tx" value={phase.hash} />
        </p>
      );
    case "confirmed":
      return (
        <p className="mt-2 flex flex-wrap items-center gap-1 text-xs text-success" data-testid="tx-confirmed">
          Confirmed: <ChainValue kind="tx" value={phase.hash} />
        </p>
      );
    case "reverted":
      return (
        <p className="mt-2 flex flex-wrap items-center gap-1 text-xs text-destructive" role="alert" data-testid="tx-reverted">
          Included but reverted — nothing changed: <ChainValue kind="tx" value={phase.hash} />
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
