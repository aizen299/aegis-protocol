"use client";

import { useConnection, useWallet } from "@solana/wallet-adapter-react";
import { Transaction, type TransactionInstruction } from "@solana/web3.js";
import { useCallback, useState } from "react";

import type { TxPhase } from "@/lib/tx";

// The Solana counterpart of useTx, with the same phases. A signature is not a success: the transaction
// is confirmed and its error inspected, and a program error is "reverted", never green.
export function useSolanaTx() {
  const { connection } = useConnection();
  const { publicKey, sendTransaction } = useWallet();
  const [phase, setPhase] = useState<TxPhase>({ kind: "idle" });

  const send = useCallback(
    async (instructions: TransactionInstruction[]) => {
      if (!publicKey) {
        setPhase({ kind: "failed", reason: "connect a Solana wallet first" });
        return;
      }
      setPhase({ kind: "signing" });
      let signature: string | undefined;
      try {
        const latest = await connection.getLatestBlockhash("confirmed");
        const tx = new Transaction({ feePayer: publicKey, ...latest }).add(...instructions);
        signature = await sendTransaction(tx, connection, { preflightCommitment: "confirmed" });
        setPhase({ kind: "pending", hash: signature });
        const result = await connection.confirmTransaction({ signature, ...latest }, "confirmed");
        setPhase(result.value.err ? { kind: "reverted", hash: signature } : { kind: "confirmed", hash: signature });
      } catch (error) {
        setPhase({ kind: "failed", reason: firstLine(error), hash: signature });
      }
    },
    [connection, publicKey, sendTransaction],
  );

  const busy = phase.kind === "signing" || phase.kind === "pending";
  return { phase, busy, send, reset: () => setPhase({ kind: "idle" }) };
}

function firstLine(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  return message.split("\n")[0]?.trim() || "the transaction could not be sent";
}
