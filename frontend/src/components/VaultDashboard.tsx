"use client";

import { ConnectButton } from "@rainbow-me/rainbowkit";
import { useAccount, useReadContract, useReadContracts } from "wagmi";

import { erc20Abi, vaultEngineAbi } from "@/lib/abi";
import { env } from "@/lib/env";
import { failed, loading, resolve, value as valueOf, type ReadState } from "@/lib/readState";
import { formatAmount, shareDecimals } from "@/lib/units";
import { Panel, Stat } from "./Panel";
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
      <Panel title="Configuration">
        <p className="text-sm text-zinc-400">
          NEXT_PUBLIC_VAULT_ADDRESS is not set. Deploy the vault and point the UI at it.
        </p>
      </Panel>
    );
  }

  return (
    <div className="flex flex-col gap-5">
      <div className="flex justify-end">
        <ConnectButton />
      </div>

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

      <Panel title="Vault">
        <Stat
          label="Total assets"
          state={vaultStat(() => formatAmount(totalAssets, decimals, symbol))}
        />
        <Stat
          label="Total shares"
          state={vaultStat(() => formatAmount(totalShares, shareScale, "shares"))}
        />
        <Stat
          label="Deposit cap"
          state={
            depositCap === 0n
              ? valueOf("uncapped")
              : vaultStat(() => formatAmount(depositCap, decimals, symbol))
          }
        />
      </Panel>

      {isConnected ? (
        <>
          <Panel title="Your position">
            <Stat
              label="Shares"
              state={resolve(sharesQuery, () => formatAmount(shares, shareScale, "shares"))}
            />
            <Stat
              label="Redeemable"
              state={resolve(claimQuery, () => formatAmount(claim, decimals, symbol))}
            />
          </Panel>

          <div className="grid gap-5 md:grid-cols-2">
            <DepositForm
              disabled={Boolean(paused)}
              decimals={decimals}
              symbol={symbol}
              assetAddress={assetAddress}
            />
            <WithdrawForm
              disabled={Boolean(withdrawalsFrozen)}
              shares={shares}
              shareScale={shareScale}
              symbol={symbol}
            />
          </div>
        </>
      ) : (
        <Panel title="Your position">
          <p className="text-sm text-zinc-400">Connect a wallet to deposit or withdraw.</p>
        </Panel>
      )}
    </div>
  );
}

function Notice({ text, tone = "warning" }: { text: string; tone?: "warning" | "error" }) {
  const palette =
    tone === "error"
      ? "border-red-900/60 bg-red-950/40 text-red-200"
      : "border-amber-900/60 bg-amber-950/40 text-amber-200";

  return (
    <p className={`rounded border px-4 py-2 text-sm ${palette}`} role="alert">
      {text}
    </p>
  );
}
