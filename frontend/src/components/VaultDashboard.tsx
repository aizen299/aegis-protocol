"use client";

import { ConnectButton } from "@rainbow-me/rainbowkit";
import { Landmark, Layers, ShieldCheck } from "lucide-react";
import { useAccount, useReadContract, useReadContracts } from "wagmi";

import { erc20Abi, vaultEngineAbi } from "@/lib/abi";
import { env } from "@/lib/env";
import { failed, loading, resolve, value as valueOf, type ReadState } from "@/lib/readState";
import { formatAmount, shareDecimals } from "@/lib/units";
import { ChainValue } from "./common/Address";
import { StatCard } from "./common/StatCard";
import { Panel, Stat } from "./Panel";
import { PageHeader } from "./shell/PageHeader";
import { DepositForm } from "./DepositForm";
import { WithdrawForm } from "./WithdrawForm";

export function VaultDashboard() {
  const { address, isConnected } = useAccount();
  const vault = env.vaultAddress;

  const vaultQuery = useReadContracts({
    contracts: [
      { address: vault, abi: vaultEngineAbi, functionName: "totalAssets" },
      { address: vault, abi: vaultEngineAbi, functionName: "totalShares" },
      { address: vault, abi: vaultEngineAbi, functionName: "depositCap" },
      { address: vault, abi: vaultEngineAbi, functionName: "paused" },
      { address: vault, abi: vaultEngineAbi, functionName: "withdrawalsFrozen" },
      { address: vault, abi: vaultEngineAbi, functionName: "virtualSharesOffset" },
      { address: vault, abi: vaultEngineAbi, functionName: "asset" },
    ],
    query: { enabled: Boolean(vault) },
  });
  const vaultData = vaultQuery.data;

  const assetAddress = vaultData?.[6]?.result;

  // Decimals come from the asset itself. Assuming 18 would misrender a six-decimal token like
  // USDC by twelve orders of magnitude.
  const assetQuery = useReadContracts({
    contracts: [
      { address: assetAddress, abi: erc20Abi, functionName: "decimals" },
      { address: assetAddress, abi: erc20Abi, functionName: "symbol" },
    ],
    query: { enabled: Boolean(assetAddress) },
  });
  const assetMeta = assetQuery.data;

  const sharesQuery = useReadContract({
    address: vault,
    abi: vaultEngineAbi,
    functionName: "sharesOf",
    args: address ? [address] : undefined,
    query: { enabled: Boolean(vault && address) },
  });
  const shares = sharesQuery.data;

  const claimQuery = useReadContract({
    address: vault,
    abi: vaultEngineAbi,
    functionName: "convertToAssets",
    args: shares !== undefined ? [shares] : undefined,
    query: { enabled: shares !== undefined },
  });
  const claim = claimQuery.data;

  const totalAssets = vaultData?.[0]?.result;
  const totalShares = vaultData?.[1]?.result;
  const depositCap = vaultData?.[2]?.result;
  const paused = vaultData?.[3]?.result;
  const withdrawalsFrozen = vaultData?.[4]?.result;
  const offset = vaultData?.[5]?.result;

  const decimals = assetMeta?.[0]?.result;
  const symbol = assetMeta?.[1]?.result;
  const shareScale = shareDecimals(decimals, offset);

  // The asset read depends on the vault read, so a vault failure is what a reader needs told about;
  // reporting both would name a consequence as if it were a second cause.
  const readsFailed = vaultQuery.isError || (!vaultQuery.isPending && vaultData === undefined);
  const failureReason = vaultQuery.error?.message;

  // A vault figure needs both reads: the number from the vault, the decimals from the asset. Either
  // one missing makes the figure unrenderable, so both are folded into one state rather than
  // letting a rendered number imply that everything behind it succeeded.
  const vaultStat = (format: () => string | undefined): ReadState => {
    if (readsFailed) return failed(failureReason);
    if (vaultQuery.isPending || assetQuery.isPending) return loading;
    if (assetQuery.isError) return failed(assetQuery.error?.message);

    const text = format();
    return text === undefined ? failed() : valueOf(text);
  };

  if (!vault) {
    return (
      <>
        <PageHeader title="Vault" />
        <Panel title="Configuration">
          <p className="text-sm text-muted-foreground">
            NEXT_PUBLIC_VAULT_ADDRESS is not set. Deploy the vault and point the UI at it.
          </p>
        </Panel>
      </>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Vault"
        description="Deposit the vault's asset for shares, priced on-chain, with a slippage floor on every fill."
        actions={<ChainValue kind="address" value={vault} />}
      />

      {readsFailed ? (
        <Notice
          tone="error"
          text={`Could not read the vault at ${vault}. The figures below are unavailable, not zero.${
            failureReason ? ` (${failureReason})` : ""
          }`}
        />
      ) : null}

      {paused ? <Notice text="Deposits are paused. Withdrawals remain open." /> : null}
      {withdrawalsFrozen ? <Notice text="Withdrawals are frozen by protocol admin." /> : null}

      <div className="grid gap-3 sm:grid-cols-3">
        <StatCard label="Total assets" icon={Landmark} state={vaultStat(() => formatAmount(totalAssets, decimals, symbol))} />
        <StatCard label="Total shares" icon={Layers} state={vaultStat(() => formatAmount(totalShares, shareScale, "shares"))} />
        <StatCard
          label="Deposit cap"
          icon={ShieldCheck}
          state={depositCap === 0n ? valueOf("uncapped") : vaultStat(() => formatAmount(depositCap, decimals, symbol))}
        />
      </div>

      {isConnected ? (
        <>
          <Panel title="Your position">
            <Stat label="Shares" state={resolve(sharesQuery, () => formatAmount(shares, shareScale, "shares"))} />
            <Stat label="Redeemable" state={resolve(claimQuery, () => formatAmount(claim, decimals, symbol))} />
          </Panel>

          <div className="grid gap-4 md:grid-cols-2">
            <DepositForm
              disabled={Boolean(paused)}
              decimals={decimals}
              symbol={symbol}
              assetAddress={assetAddress}
              offset={offset}
            />
            <WithdrawForm
              disabled={Boolean(withdrawalsFrozen)}
              shares={shares}
              shareScale={shareScale}
              symbol={symbol}
              decimals={decimals}
            />
          </div>
        </>
      ) : (
        <Panel title="Your position">
          <div className="flex flex-col items-center gap-3 py-6 text-center">
            <p className="text-sm text-muted-foreground">Connect a wallet to deposit or withdraw.</p>
            <ConnectButton />
          </div>
        </Panel>
      )}
    </div>
  );
}

function Notice({ text, tone = "warning" }: { text: string; tone?: "warning" | "error" }) {
  const palette =
    tone === "error"
      ? "border-destructive/40 bg-destructive/10 text-destructive"
      : "border-warning/40 bg-warning/10 text-warning";

  return (
    <p className={`rounded-lg border px-4 py-3 text-sm ${palette}`} role="alert">
      {text}
    </p>
  );
}
