"use client";

import { ConnectButton } from "@rainbow-me/rainbowkit";
import { useAccount, useReadContract, useReadContracts } from "wagmi";

import { erc20Abi, vaultEngineAbi } from "@/lib/abi";
import { env } from "@/lib/env";
import { formatAmount, shareDecimals } from "@/lib/units";
import { Panel, Stat } from "./Panel";
import { DepositForm } from "./DepositForm";
import { WithdrawForm } from "./WithdrawForm";

export function VaultDashboard() {
  const { address, isConnected } = useAccount();
  const vault = env.vaultAddress;

  const { data: vaultData } = useReadContracts({
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

  const assetAddress = vaultData?.[6]?.result;

  // Decimals come from the asset itself. Assuming 18 would misrender a six-decimal token like
  // USDC by twelve orders of magnitude.
  const { data: assetMeta } = useReadContracts({
    contracts: [
      { address: assetAddress, abi: erc20Abi, functionName: "decimals" },
      { address: assetAddress, abi: erc20Abi, functionName: "symbol" },
    ],
    query: { enabled: Boolean(assetAddress) },
  });

  const { data: shares } = useReadContract({
    address: vault,
    abi: vaultEngineAbi,
    functionName: "sharesOf",
    args: address ? [address] : undefined,
    query: { enabled: Boolean(vault && address) },
  });

  const { data: claim } = useReadContract({
    address: vault,
    abi: vaultEngineAbi,
    functionName: "convertToAssets",
    args: shares !== undefined ? [shares] : undefined,
    query: { enabled: shares !== undefined },
  });

  const totalAssets = vaultData?.[0]?.result;
  const totalShares = vaultData?.[1]?.result;
  const depositCap = vaultData?.[2]?.result;
  const paused = vaultData?.[3]?.result;
  const withdrawalsFrozen = vaultData?.[4]?.result;
  const offset = vaultData?.[5]?.result;

  const decimals = assetMeta?.[0]?.result;
  const symbol = assetMeta?.[1]?.result;
  const shareScale = shareDecimals(decimals, offset);

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

      {paused ? <Notice text="Deposits are paused. Withdrawals remain open." /> : null}
      {withdrawalsFrozen ? <Notice text="Withdrawals are frozen by protocol admin." /> : null}

      <Panel title="Vault">
        <Stat label="Total assets" value={formatAmount(totalAssets, decimals, symbol)} />
        <Stat label="Total shares" value={formatAmount(totalShares, shareScale, "shares")} />
        <Stat
          label="Deposit cap"
          value={depositCap === 0n ? "uncapped" : formatAmount(depositCap, decimals, symbol)}
        />
      </Panel>

      {isConnected ? (
        <>
          <Panel title="Your position">
            <Stat label="Shares" value={formatAmount(shares, shareScale, "shares")} />
            <Stat label="Redeemable" value={formatAmount(claim, decimals, symbol)} />
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

function Notice({ text }: { text: string }) {
  return (
    <p className="rounded border border-amber-900/60 bg-amber-950/40 px-4 py-2 text-sm text-amber-200">
      {text}
    </p>
  );
}
