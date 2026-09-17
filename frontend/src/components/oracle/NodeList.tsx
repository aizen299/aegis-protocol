"use client";

import type { GridColDef } from "@mui/x-data-grid";
import { ShieldAlert, Users, Wallet } from "lucide-react";

import { Async } from "@/components/Async";
import { ChainValue } from "@/components/common/Address";
import { DataTable } from "@/components/common/DataTable";
import { StatCard } from "@/components/common/StatCard";
import { Panel } from "@/components/Panel";
import { PageHeader } from "@/components/shell/PageHeader";
import { StateBadge } from "@/components/StateBadge";
import type { OracleNode } from "@/lib/api";
import { relativeToNow } from "@/lib/format";
import { useOracleNodes } from "@/lib/queries";
import { loading, value as valueOf, failed } from "@/lib/readState";
import { formatRaw } from "@/lib/units";

const columns: GridColDef<OracleNode>[] = [
  {
    field: "address",
    headerName: "Node",
    flex: 1.4,
    minWidth: 170,
    sortable: false,
    renderCell: ({ row }) => <ChainValue kind="address" value={row.address} />,
  },
  {
    field: "stakedAmount",
    headerName: "Staked",
    flex: 1,
    minWidth: 120,
    sortComparator: (a: string, b: string) => (BigInt(a) < BigInt(b) ? -1 : BigInt(a) > BigInt(b) ? 1 : 0),
    renderCell: ({ row }) => <span className="tabular font-mono">{formatRaw(row.stakedAmount, row.decimals) ?? "unavailable"}</span>,
  },
  {
    field: "slashedTotal",
    headerName: "Slashed",
    flex: 0.8,
    minWidth: 100,
    sortComparator: (a: string, b: string) => (BigInt(a) < BigInt(b) ? -1 : BigInt(a) > BigInt(b) ? 1 : 0),
    renderCell: ({ row }) => (
      <span className={row.slashedTotal !== "0" ? "tabular font-mono text-destructive" : "tabular font-mono"}>
        {formatRaw(row.slashedTotal, row.decimals) ?? "unavailable"}
      </span>
    ),
  },
  {
    field: "missedRounds",
    headerName: "Missed",
    type: "number",
    width: 90,
    renderCell: ({ row }) => (
      <span className="tabular font-mono" title="Slashable misses only — see the note below for what is excused.">
        {row.missedRounds}
      </span>
    ),
  },
  {
    field: "pendingUnstake",
    headerName: "Unbonding",
    flex: 0.9,
    minWidth: 120,
    sortable: false,
    renderCell: ({ row }) =>
      row.pendingUnstake === "0" ? (
        <span className="text-muted-foreground">—</span>
      ) : (
        <span className="tabular font-mono">
          {formatRaw(row.pendingUnstake, row.decimals)} <span className="text-muted-foreground">{relativeToNow(row.claimableAt)}</span>
        </span>
      ),
  },
  {
    field: "active",
    headerName: "Status",
    width: 110,
    renderCell: ({ row }) => <StateBadge state={row.active ? "active" : "inactive"} />,
  },
];

export function NodeList() {
  const nodes = useOracleNodes();
  const items = nodes.data?.items;
  const summary = (f: (items: OracleNode[]) => string | undefined) =>
    nodes.isPending ? loading : items ? (f(items) === undefined ? failed() : valueOf(f(items)!)) : failed(nodes.error?.message);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Oracle nodes" description="Staked operators signing price submissions, and what each has been slashed." />

      <div className="grid gap-3 sm:grid-cols-3">
        <StatCard label="Active nodes" icon={Users} state={summary((n) => `${n.filter((x) => x.active).length} of ${n.length}`)} />
        <StatCard
          label="Total staked"
          icon={Wallet}
          state={summary((n) => (n.length ? formatRaw(n.reduce((s, x) => s + BigInt(x.stakedAmount), 0n).toString(), n[0]!.decimals) : "0"))}
        />
        <StatCard
          label="Total slashed"
          icon={ShieldAlert}
          state={summary((n) => (n.length ? formatRaw(n.reduce((s, x) => s + BigInt(x.slashedTotal), 0n).toString(), n[0]!.decimals) : "0"))}
        />
      </div>

      <Panel title="Nodes">
        <Async query={{ ...nodes, data: items }} empty="No nodes are registered.">
          {(rows) => <DataTable rows={rows} columns={columns} getRowId={(r) => r.address} />}
        </Async>
      </Panel>

      {/* The note this replaced said misses were "recorded but not penalised". The second half was
          true and the first was not: nothing wrote the count, so every node showed zero. The rules
          are stated here so an operator can predict their own penalty. See
          docs/v1.3-missed-round-slashing-plan.md. */}
      <div className="rounded-lg border bg-muted/40 px-4 py-3 text-sm text-muted-foreground" data-testid="missed-round-rules">
        <p>
          A missed round costs 0.5% of stake. A third consecutive miss costs 10% instead, and the count starts again.
        </p>
        <p className="mt-1">
          Not counted as a miss: a round nobody submitted to, a round settled before its deadline, and a round the node was
          removed from by an admin or for low stake before its deadline. Requesting to unstake is not an excuse.
        </p>
      </div>
    </div>
  );
}
