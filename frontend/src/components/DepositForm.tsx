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
        className="mb-3 flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 font-mono text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      />
      {parsed !== null ? (
        <p className="mb-3 text-xs text-muted-foreground" data-testid="deposit-floor">
          {floorText === undefined ? (
            "Cannot price this deposit yet — the transaction is disabled until it can."
          ) : (
            <>
              Reverts below <span className="font-mono text-muted-foreground">{floorText}</span>, a{" "}
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
