"use client";

import type { GridColDef } from "@mui/x-data-grid";
import { Activity, CheckCircle2, Clock } from "lucide-react";
import Link from "next/link";
import { formatUnits } from "viem";

import { Async } from "@/components/Async";
import { LineChart } from "@/components/common/LineChart";
import { DataTable } from "@/components/common/DataTable";
import { StatCard } from "@/components/common/StatCard";
import { Panel } from "@/components/Panel";
import { PageHeader } from "@/components/shell/PageHeader";
import { StateBadge } from "@/components/StateBadge";
import type { OracleRound } from "@/lib/api";
import { useChainHref } from "@/lib/chainContext";
import { formatTime, relativeToNow } from "@/lib/format";
import { useOracleFeed, useOracleRounds } from "@/lib/queries";
import { failed, loading, resolve, value as valueOf } from "@/lib/readState";
import { formatRaw } from "@/lib/units";

export function RoundList({ feedId }: { feedId: string }) {
  const chainHref = useChainHref();
  const feed = useOracleFeed(feedId);
  const rounds = useOracleRounds(feedId);
  const items = rounds.data?.items;
  const settled = (items ?? []).filter((r) => r.state.toLowerCase() === "settled");
  const latest = settled[0];

  const columns: GridColDef<OracleRound>[] = [
    {
      field: "roundId",
      headerName: "Round",
      width: 100,
      renderCell: ({ row }) => (
        <Link className="font-mono text-info hover:underline" href={chainHref(`/oracle/rounds/${row.roundId}`)}>
          #{row.roundId}
        </Link>
      ),
    },
    { field: "state", headerName: "State", width: 130, renderCell: ({ row }) => <StateBadge state={row.state} /> },
    {
      field: "aggregatedValue",
      headerName: "Value",
      flex: 1,
      minWidth: 120,
      sortable: false,
      renderCell: ({ row }) => (
        <span className="tabular font-mono">
          {row.state.toLowerCase() === "settled" ? formatRaw(row.aggregatedValue, row.decimals) ?? "unavailable" : "—"}
        </span>
      ),
    },
    {
      field: "submissionCount",
      headerName: "Submissions",
      width: 130,
      renderCell: ({ row }) => (
        <span className="tabular font-mono">
          {row.submissionCount} / {row.eligibleCount}
        </span>
      ),
    },
    { field: "openedAt", headerName: "Opened", flex: 1, minWidth: 170, renderCell: ({ row }) => formatTime(row.openedAt) },
  ];

  const points = [...settled]
    .reverse()
    .filter((r) => r.settledAt)
    .map((r) => ({ t: new Date(r.settledAt!).getTime(), v: Number(formatUnits(BigInt(r.aggregatedValue), r.decimals)), label: formatTime(r.settledAt) }));

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title={feed.data?.name ?? "Feed"} description="Every round opened for this feed, newest first." />

      <div className="grid gap-3 sm:grid-cols-3">
        <StatCard
          label="Latest value"
          icon={Activity}
          state={rounds.isPending ? loading : latest ? resolve(rounds, () => formatRaw(latest.aggregatedValue, latest.decimals)) : rounds.isError ? failed(rounds.error?.message) : valueOf("no settled round")}
        />
        <StatCard label="Settled" icon={Clock} state={rounds.isPending ? loading : valueOf(latest ? relativeToNow(latest.settledAt) : "—")} />
        <StatCard label="Rounds" icon={CheckCircle2} state={resolve(rounds, () => `${settled.length} settled of ${items?.length ?? 0}`)} />
      </div>

      {points.length >= 2 ? (
        <Panel title="Settled values">
          <LineChart points={points} title={`${feed.data?.name ?? "Feed"} settled values`} />
        </Panel>
      ) : null}

      <Panel title="Rounds">
        <Async query={{ ...rounds, data: items }} empty="No rounds have opened for this feed.">
          {(rows) => <DataTable rows={rows} columns={columns} getRowId={(r) => r.roundId} />}
        </Async>
      </Panel>
    </div>
  );
}
