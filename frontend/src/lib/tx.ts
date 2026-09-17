"use client";

import { useWaitForTransactionReceipt, useWriteContract } from "wagmi";

// A transaction's lifecycle, with the states that must never share a rendering kept apart.
//
// "Submitted" is not "succeeded": a hash exists the moment the wallet broadcasts, long before
// inclusion. And "mined" is not "succeeded" either — a reverted transaction has a receipt too, and
// wagmi resolves it rather than raising an error. The UI previously went green on the hash alone,
// so a deposit reverted by its own slippage bound looked like a deposit that landed.
// A hash is an EVM transaction hash or a Solana signature, each in its chain's canonical form.
export type TxPhase =
  | { kind: "idle" }
  | { kind: "signing" }
  | { kind: "pending"; hash: string }
  | { kind: "confirmed"; hash: string }
  | { kind: "reverted"; hash: string }
  | { kind: "failed"; reason: string; hash?: string };

export function txPhase(input: {
  signing: boolean;
  writeError?: { message?: string } | null;
  hash?: `0x${string}`;
  receiptPending: boolean;
  receiptStatus?: "success" | "reverted";
  receiptError?: { message?: string } | null;
}): TxPhase {
  if (input.writeError) return { kind: "failed", reason: firstLine(input.writeError.message) };
  if (input.signing) return { kind: "signing" };
  if (!input.hash) return { kind: "idle" };

  if (input.receiptError) {
    return { kind: "failed", reason: firstLine(input.receiptError.message), hash: input.hash };
  }
  if (input.receiptStatus === "reverted") return { kind: "reverted", hash: input.hash };
  if (input.receiptStatus === "success") return { kind: "confirmed", hash: input.hash };

  // A hash with no receipt status is in flight, whatever receiptPending claims: a missing status
  // must never be read as success.
  return { kind: "pending", hash: input.hash };
}

// Wallet errors carry the full RPC payload; the first line is the part a person can act on.
function firstLine(message?: string): string {
  const line = message?.split("\n")[0]?.trim();
  return line || "the transaction could not be sent";
}

export function useTx() {
  const write = useWriteContract();
  const receipt = useWaitForTransactionReceipt({ hash: write.data });

  const phase = txPhase({
    signing: write.isPending,
    writeError: write.error,
    hash: write.data,
    receiptPending: receipt.isLoading,
    receiptStatus: receipt.data?.status,
    receiptError: receipt.error,
  });

  const busy = phase.kind === "signing" || phase.kind === "pending";

  return { phase, busy, writeContract: write.writeContract, reset: write.reset };
}
