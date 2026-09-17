"use client";

import { useState } from "react";
import { formatUnits } from "viem";
import { useAccount, useReadContract } from "wagmi";

import { vaultEngineAbi } from "@/lib/abi";
import { env } from "@/lib/env";
import { DEFAULT_TOLERANCE_BPS, formatTolerance, minimumOut } from "@/lib/slippage";
import { formatAmount, parseAmount } from "@/lib/units";
import { useTx } from "@/lib/tx";
import { Panel } from "./Panel";
import { TxButton, TxStatus } from "./TxButton";

// withdraw() is denominated in shares, not assets — see IVaultEngine. Shares carry the asset's
// decimals plus the vault's virtual-shares offset, which the dashboard reads from the contract.
export function WithdrawForm({
  disabled,
  shares,
  shareScale,
  symbol,
  decimals,
}: {
  disabled: boolean;
  shares: bigint | undefined;
  shareScale: number | undefined;
  symbol: string | undefined;
  decimals: number | undefined;
}) {
  const { address } = useAccount();
  const [amount, setAmount] = useState("");
  const { phase, busy, writeContract } = useTx();

  const parsed = parseAmount(amount, shareScale);
  const exceedsBalance = parsed !== null && shares !== undefined && parsed > shares;

  const { data: expectedAssets } = useReadContract({
    address: env.vaultAddress,
    abi: vaultEngineAbi,
    functionName: "convertToAssets",
    args: parsed !== null ? [parsed] : undefined,
    query: { enabled: parsed !== null },
  });

  const floor = minimumOut(expectedAssets, DEFAULT_TOLERANCE_BPS);
  const floorText = formatAmount(floor, decimals, symbol);

  function submit() {
    if (parsed === null || !address || floor === undefined) return;
    writeContract({
      address: env.vaultAddress,
      abi: vaultEngineAbi,
      functionName: "withdraw",
      args: [parsed, address, floor],
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
          className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 font-mono text-sm shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        />
        <button
          type="button"
          onClick={() =>
            shares !== undefined &&
            shareScale !== undefined &&
            setAmount(formatUnits(shares, shareScale))
          }
          className="shrink-0 rounded-md border px-2.5 py-2 text-xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          Max
        </button>
      </div>
      {parsed !== null && !exceedsBalance ? (
        <p className="mb-3 text-xs text-muted-foreground" data-testid="withdraw-floor">
          {floorText === undefined ? (
            "Cannot price this withdrawal yet — the transaction is disabled until it can."
          ) : (
            <>
              Reverts below <span className="font-mono text-muted-foreground">{floorText}</span>, a{" "}
              {formatTolerance(DEFAULT_TOLERANCE_BPS)} tolerance on the current rate.
            </>
          )}
        </p>
      ) : null}
      <TxButton
        disabled={disabled || parsed === null || exceedsBalance || floor === undefined}
        pending={busy}
        onClick={submit}
      >
        {exceedsBalance ? "Exceeds balance" : `Withdraw${symbol ? ` (${symbol})` : ""}`}
      </TxButton>
      <TxStatus phase={phase} />
    </Panel>
  );
}
