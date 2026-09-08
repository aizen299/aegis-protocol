"use client";

import { useState } from "react";
import { useAccount, useReadContract, useWaitForTransactionReceipt, useWriteContract } from "wagmi";

import { erc20Abi, vaultEngineAbi } from "@/lib/abi";
import { env } from "@/lib/env";
import { parseAmount } from "@/lib/units";
import { Panel } from "./Panel";
import { TxButton, TxStatus } from "./TxButton";

export function DepositForm({
  disabled,
  decimals,
  symbol,
  assetAddress,
}: {
  disabled: boolean;
  decimals: number | undefined;
  symbol: string | undefined;
  assetAddress: `0x${string}` | undefined;
}) {
  const { address } = useAccount();
  const [amount, setAmount] = useState("");
  const { writeContract, data: hash, error, isPending } = useWriteContract();
  const { isLoading: confirming } = useWaitForTransactionReceipt({ hash });

  const parsed = parseAmount(amount, decimals);

  const { data: allowance } = useReadContract({
    address: assetAddress,
    abi: erc20Abi,
    functionName: "allowance",
    args: address ? [address, env.vaultAddress] : undefined,
    query: { enabled: Boolean(address && assetAddress) },
  });

  const needsApproval = parsed !== null && allowance !== undefined && allowance < parsed;

  function submit() {
    if (parsed === null || !address || !assetAddress) return;

    if (needsApproval) {
      writeContract({
        address: assetAddress,
        abi: erc20Abi,
        functionName: "approve",
        args: [env.vaultAddress, parsed],
      });
      return;
    }

    writeContract({
      address: env.vaultAddress,
      abi: vaultEngineAbi,
      functionName: "deposit",
      args: [parsed, address],
    });
  }

  return (
    <Panel title="Deposit">
      <input
        value={amount}
        onChange={(e) => setAmount(e.target.value)}
        inputMode="decimal"
        placeholder={`0.0 ${symbol ?? ""}`.trim()}
        className="mb-3 w-full rounded border border-edge bg-surface px-3 py-2 font-mono text-sm outline-none focus:border-zinc-500"
      />
      <TxButton
        disabled={disabled || parsed === null}
        pending={isPending || confirming}
        onClick={submit}
      >
        {needsApproval ? "Approve" : "Deposit"}
      </TxButton>
      <TxStatus error={error} hash={hash} />
    </Panel>
  );
}
