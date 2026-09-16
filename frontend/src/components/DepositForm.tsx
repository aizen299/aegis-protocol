"use client";

import { useState } from "react";
import { useAccount, useReadContract } from "wagmi";

import { erc20Abi, vaultEngineAbi } from "@/lib/abi";
import { env } from "@/lib/env";
import { DEFAULT_TOLERANCE_BPS, formatTolerance, minimumOut } from "@/lib/slippage";
import { formatAmount, parseAmount, shareDecimals } from "@/lib/units";
import { useTx } from "@/lib/tx";
import { Panel } from "./Panel";
import { TxButton, TxStatus } from "./TxButton";

export function DepositForm({
  disabled,
  decimals,
  symbol,
  assetAddress,
  offset,
}: {
  disabled: boolean;
  decimals: number | undefined;
  symbol: string | undefined;
  assetAddress: `0x${string}` | undefined;
  offset: number | undefined;
}) {
  const { address } = useAccount();
  const [amount, setAmount] = useState("");
  const { phase, busy, writeContract } = useTx();

  const parsed = parseAmount(amount, decimals);

  const { data: allowance } = useReadContract({
    address: assetAddress,
    abi: erc20Abi,
    functionName: "allowance",
    args: address ? [address, env.vaultAddress] : undefined,
    query: { enabled: Boolean(address && assetAddress) },
  });

  const needsApproval = parsed !== null && allowance !== undefined && allowance < parsed;

  // The rate at which this deposit would fill right now. The floor is derived from it, so a stale
  // quote produces a stale floor and the transaction reverts rather than filling badly.
  const { data: expectedShares } = useReadContract({
    address: env.vaultAddress,
    abi: vaultEngineAbi,
    functionName: "convertToShares",
    args: parsed !== null ? [parsed] : undefined,
    query: { enabled: parsed !== null },
  });

  const floor = minimumOut(expectedShares, DEFAULT_TOLERANCE_BPS);
  const floorText = formatAmount(floor, shareDecimals(decimals, offset), "shares");

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

    if (floor === undefined) return;

    writeContract({
      address: env.vaultAddress,
      abi: vaultEngineAbi,
      functionName: "deposit",
      args: [parsed, address, floor],
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
      {parsed !== null ? (
        <p className="mb-3 text-xs text-zinc-500" data-testid="deposit-floor">
          {floorText === undefined ? (
            "Cannot price this deposit yet — the transaction is disabled until it can."
          ) : (
            <>
              Reverts below <span className="font-mono text-zinc-400">{floorText}</span>, a{" "}
              {formatTolerance(DEFAULT_TOLERANCE_BPS)} tolerance on the current rate.
            </>
          )}
        </p>
      ) : null}
      <TxButton
        disabled={disabled || parsed === null || (!needsApproval && floor === undefined)}
        pending={busy}
        onClick={submit}
      >
        {needsApproval ? "Approve" : "Deposit"}
      </TxButton>
      <TxStatus phase={phase} />
    </Panel>
  );
}
