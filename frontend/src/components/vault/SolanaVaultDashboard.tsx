"use client";

import { Coins, Landmark, Layers, ShieldCheck } from "lucide-react";
import { useState } from "react";

import { Async } from "@/components/Async";
import { ChainValue } from "@/components/common/Address";
import { StatCard } from "@/components/common/StatCard";
import { Panel, Stat } from "@/components/Panel";
import { PageHeader } from "@/components/shell/PageHeader";
import { TxButton, TxStatus } from "@/components/TxButton";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { SolanaWalletButton } from "@/components/wallet/SolanaWalletButton";
import { formatTime } from "@/lib/format";
import { useVaultDeposits } from "@/lib/queries";
import { failed, loading, resolve, value as valueOf, type ReadState } from "@/lib/readState";
import { DEFAULT_TOLERANCE_BPS, formatTolerance, minimumOut } from "@/lib/slippage";
import { useSolanaTx } from "@/lib/solana/tx";
import { useSolanaVault } from "@/lib/solana/useSolanaVault";
import { convertToAssets, convertToShares, depositInstruction, withdrawInstruction } from "@/lib/solana/vault";
import { formatAmount, parseAmount } from "@/lib/units";

export function SolanaVaultDashboard() {
  const { mint, vault, state, position, owner } = useSolanaVault();

  if (!mint || !vault) {
    return (
      <>
        <PageHeader title="Solana vault" />
        <Panel title="Configuration">
          <p className="text-sm text-muted-foreground">
            NEXT_PUBLIC_SOLANA_VAULT_MINT is not set to a valid mint. The vault is derived from its mint, so the UI needs it.
          </p>
        </Panel>
      </>
    );
  }

  const s = state.data;
  const stat = (format: (v: NonNullable<typeof s>) => string | undefined): ReadState =>
    state.isPending ? loading : s ? (format(s) === undefined ? failed() : valueOf(format(s)!)) : failed(state.error?.message);
  const shareScale = s ? s.decimals + s.shareOffset : undefined;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Solana vault"
        description="An idle-asset vault on Solana. Shares are priced by the program, and every deposit and withdrawal is bounded by a slippage floor."
        actions={<ChainValue kind="address" value={vault.toBase58()} />}
      />

      {state.isError ? (
        <p role="alert" className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm">
          Could not read the vault. The figures below are unavailable, not zero. ({state.error.message})
        </p>
      ) : null}
      {s?.paused ? <Notice text="Deposits are paused. Withdrawals remain open." /> : null}
      {s?.withdrawalsFrozen ? <Notice text="Withdrawals are frozen by the vault's pauser." /> : null}

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard label="Total assets" icon={Landmark} state={stat((v) => formatAmount(v.totalAssets, v.decimals))} />
        <StatCard label="Total shares" icon={Layers} state={stat((v) => formatAmount(v.totalShares, v.decimals + v.shareOffset))} />
        <StatCard label="Deposit cap" icon={ShieldCheck} state={stat((v) => (v.depositCap === 0n ? "uncapped" : formatAmount(v.depositCap, v.decimals)))} />
        <StatCard label="Minimum deposit" icon={Coins} state={stat((v) => formatAmount(v.minDeposit, v.decimals))} />
      </div>

      {owner ? (
        <div className="grid gap-4 lg:grid-cols-[1fr_1.2fr]">
          <Panel title="Your position">
            <Stat label="Shares" state={resolve(position, () => formatAmount(position.data?.shares, shareScale, "shares"))} />
            <Stat
              label="Redeemable"
              state={resolve(position, () =>
                s && position.data ? formatAmount(convertToAssets(position.data.shares, s.totalAssets, s.totalShares, s.shareOffset), s.decimals) : undefined,
              )}
            />
            <Stat
              label="Wallet balance"
              state={resolve(position, () =>
                position.data?.walletBalance === null ? "no token account" : formatAmount(position.data?.walletBalance ?? undefined, s?.decimals),
              )}
            />
          </Panel>
          <Panel title="Move funds">
            <Tabs defaultValue="deposit">
              <TabsList className="mb-4 grid w-full grid-cols-2">
                <TabsTrigger value="deposit">Deposit</TabsTrigger>
                <TabsTrigger value="withdraw">Withdraw</TabsTrigger>
              </TabsList>
              <TabsContent value="deposit">
                <SolanaDeposit onDone={() => void Promise.all([state.refetch(), position.refetch()])} />
              </TabsContent>
              <TabsContent value="withdraw">
                <SolanaWithdraw onDone={() => void Promise.all([state.refetch(), position.refetch()])} />
              </TabsContent>
            </Tabs>
          </Panel>
        </div>
      ) : (
        <Panel title="Your position">
          <div className="flex flex-col items-center gap-3 py-6 text-center">
            <p className="text-sm text-muted-foreground">Connect a Solana wallet to see your position, deposit, or withdraw.</p>
            <SolanaWalletButton />
          </div>
        </Panel>
      )}

      {owner ? <DepositHistory owner={owner.toBase58()} /> : null}
    </div>
  );
}

function SolanaDeposit({ onDone }: { onDone: () => void }) {
  const { mint, state, position, owner } = useSolanaVault();
  const tx = useSolanaTx();
  const [amount, setAmount] = useState("");
  const s = state.data;
  const parsed = parseAmount(amount, s?.decimals);

  const expected = parsed !== null && s ? convertToShares(parsed, s.totalAssets, s.totalShares, s.shareOffset) : undefined;
  const floor = minimumOut(expected, DEFAULT_TOLERANCE_BPS);
  const problem =
    !s || parsed === null
      ? undefined
      : s.paused
        ? "Deposits are paused."
        : parsed < s.minDeposit
          ? `The minimum deposit is ${formatAmount(s.minDeposit, s.decimals)}.`
          : s.depositCap !== 0n && s.totalAssets + parsed > s.depositCap
            ? "This deposit would exceed the vault's cap."
            : position.data?.walletBalance === null
              ? "Your wallet has no token account for this asset."
              : position.data && position.data.walletBalance! < parsed
                ? "Your wallet balance is below this amount."
                : floor === undefined || floor === 0n
                  ? "This amount is too small to mint a share."
                  : undefined;

  return (
    <div className="flex flex-col gap-3">
      <div className="grid gap-1.5">
        <Label htmlFor="sol-deposit">Amount</Label>
        <Input id="sol-deposit" value={amount} onChange={(e) => setAmount(e.target.value)} inputMode="decimal" placeholder="0.0" className="font-mono" />
      </div>
      {parsed !== null && s ? (
        <p className="text-xs text-muted-foreground" data-testid="solana-deposit-floor">
          {problem ?? (
            <>
              Receives about <span className="font-mono text-foreground">{formatAmount(expected, s.decimals + s.shareOffset, "shares")}</span>, and reverts below{" "}
              <span className="font-mono text-foreground">{formatAmount(floor, s.decimals + s.shareOffset, "shares")}</span> ({formatTolerance(DEFAULT_TOLERANCE_BPS)} tolerance).
            </>
          )}
        </p>
      ) : null}
      <TxButton
        disabled={parsed === null || problem !== undefined || !mint || !owner}
        pending={tx.busy}
        onClick={async () => {
          if (!mint || !owner || parsed === null || floor === undefined) return;
          await tx.send([depositInstruction({ mint, depositor: owner, amount: parsed, minShares: floor })]);
          onDone();
        }}
      >
        Deposit
      </TxButton>
      <TxStatus phase={tx.phase} />
    </div>
  );
}

function SolanaWithdraw({ onDone }: { onDone: () => void }) {
  const { mint, state, position, owner } = useSolanaVault();
  const tx = useSolanaTx();
  const [shares, setShares] = useState("");
  const s = state.data;
  const scale = s ? s.decimals + s.shareOffset : undefined;
  const parsed = parseAmount(shares, scale);
  const held = position.data?.shares;

  const expected = parsed !== null && s ? convertToAssets(parsed, s.totalAssets, s.totalShares, s.shareOffset) : undefined;
  const floor = minimumOut(expected, DEFAULT_TOLERANCE_BPS);
  const problem =
    !s || parsed === null
      ? undefined
      : s.withdrawalsFrozen
        ? "Withdrawals are frozen."
        : held !== undefined && parsed > held
          ? "You hold fewer shares than this."
          : position.data?.walletBalance === null
            ? "Your wallet has no token account to receive into."
            : undefined;

  return (
    <div className="flex flex-col gap-3">
      <div className="grid gap-1.5">
        <div className="flex items-center justify-between">
          <Label htmlFor="sol-withdraw">Shares</Label>
          {held !== undefined && held > 0n && scale !== undefined ? (
            <button type="button" className="text-xs text-primary hover:underline" onClick={() => setShares(formatAmountPlain(held, scale))}>
              Max
            </button>
          ) : null}
        </div>
        <Input id="sol-withdraw" value={shares} onChange={(e) => setShares(e.target.value)} inputMode="decimal" placeholder="0.0" className="font-mono" />
      </div>
      {parsed !== null && s ? (
        <p className="text-xs text-muted-foreground">
          {problem ?? (
            <>
              Returns about <span className="font-mono text-foreground">{formatAmount(expected, s.decimals)}</span>, and reverts below{" "}
              <span className="font-mono text-foreground">{formatAmount(floor, s.decimals)}</span>.
            </>
          )}
        </p>
      ) : null}
      <TxButton
        disabled={parsed === null || problem !== undefined || !mint || !owner || floor === undefined}
        pending={tx.busy}
        onClick={async () => {
          if (!mint || !owner || parsed === null || floor === undefined) return;
          await tx.send([withdrawInstruction({ mint, owner, shares: parsed, minAmount: floor })]);
          onDone();
        }}
      >
        Withdraw
      </TxButton>
      <TxStatus phase={tx.phase} />
    </div>
  );
}

// Unlocalised, so it parses back exactly.
function formatAmountPlain(raw: bigint, decimals: number): string {
  const base = 10n ** BigInt(decimals);
  const frac = (raw % base).toString().padStart(decimals, "0").replace(/0+$/, "");
  return frac ? `${raw / base}.${frac}` : `${raw / base}`;
}

function DepositHistory({ owner }: { owner: string }) {
  const deposits = useVaultDeposits(owner);
  return (
    <Panel title="Your indexed deposits">
      <Async query={{ ...deposits, data: deposits.data?.items }} empty="The indexer has recorded no deposit from this wallet yet.">
        {(items) => (
          <ul className="divide-y">
            {items.map((d) => (
              <li key={`${d.txHash}:${d.logIndex}`} className="flex flex-wrap items-center justify-between gap-2 py-2.5 text-sm">
                <span className="tabular font-mono">{formatAmount(BigInt(d.amount), d.decimals)}</span>
                <ChainValue kind="tx" value={d.txHash} />
                <span className="text-muted-foreground">{formatTime(d.depositedAt)}</span>
              </li>
            ))}
          </ul>
        )}
      </Async>
    </Panel>
  );
}

function Notice({ text }: { text: string }) {
  return (
    <p className="rounded-lg border border-warning/40 bg-warning/10 px-4 py-3 text-sm text-warning" role="alert">
      {text}
    </p>
  );
}
