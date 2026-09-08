"use client";

import { useState } from "react";
import { formatUnits } from "viem";
import { useAccount, useWaitForTransactionReceipt, useWriteContract } from "wagmi";

import { vaultEngineAbi } from "@/lib/abi";
import { env } from "@/lib/env";
import { parseAmount } from "@/lib/units";
import { Panel } from "./Panel";
import { TxButton, TxStatus } from "./TxButton";

// withdraw() is denominated in shares, not assets — see IVaultEngine. Shares carry the asset's
// decimals plus the vault's virtual-shares offset, which the dashboard reads from the contract.
export function WithdrawForm({
  disabled,
  shares,
  shareScale,
  symbol,
}: {
  disabled: boolean;
  shares: bigint | undefined;
  shareScale: number | undefined;
  symbol: string | undefined;
}) {
  const { address } = useAccount();
  const [amount, setAmount] = useState("");
  const { writeContract, data: hash, error, isPending } = useWriteContract();
  const { isLoading: confirming } = useWaitForTransactionReceipt({ hash });

  const parsed = parseAmount(amount, shareScale);
  const exceedsBalance = parsed !== null && shares !== undefined && parsed > shares;

  function submit() {
    if (parsed === null || !address) return;
    writeContract({
      address: env.vaultAddress,
      abi: vaultEngineAbi,
      functionName: "withdraw",
      args: [parsed, address],
    });
  }

  return (
    <Panel title="Withdraw">
      <div className="mb-3 flex items-center gap-2">
        <input
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          inputMode="decimal"
          placeholder="0.0 shares"
          className="w-full rounded border border-edge bg-surface px-3 py-2 font-mono text-sm outline-none focus:border-zinc-500"
        />
        <button
          type="button"
          onClick={() =>
            shares !== undefined &&
            shareScale !== undefined &&
            setAmount(formatUnits(shares, shareScale))
          }
          className="shrink-0 rounded border border-edge px-2 py-2 text-xs text-zinc-400 hover:text-zinc-200"
        >
          Max
        </button>
      </div>
      <TxButton
        disabled={disabled || parsed === null || exceedsBalance}
        pending={isPending || confirming}
        onClick={submit}
      >
        {exceedsBalance ? "Exceeds balance" : `Withdraw${symbol ? ` (${symbol})` : ""}`}
      </TxButton>
      <TxStatus error={error} hash={hash} />
    </Panel>
  );
}
