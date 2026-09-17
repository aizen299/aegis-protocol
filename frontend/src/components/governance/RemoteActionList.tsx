"use client";

import type { GridColDef } from "@mui/x-data-grid";
import { ArrowUpRight, CheckCircle2, Clock, XCircle } from "lucide-react";
import Link from "next/link";
import { useState } from "react";

import { Async } from "@/components/Async";
import { ChainValue } from "@/components/common/Address";
import { DataTable } from "@/components/common/DataTable";
import { StatCard } from "@/components/common/StatCard";
import { Panel } from "@/components/Panel";
import { PageHeader } from "@/components/shell/PageHeader";
import { StateBadge } from "@/components/StateBadge";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { RemoteAction } from "@/lib/api";
import { useServedChains, withChain } from "@/lib/chainContext";
import { chainById } from "@/lib/chains";
import { formatTime, relativeToNow } from "@/lib/format";
import { useProposals, useRemoteActions } from "@/lib/queries";
import { loading, value as valueOf, failed } from "@/lib/readState";

export function RemoteActionList() {
  const [status, setStatus] = useState("");
  const all = useRemoteActions();
  const actions = useRemoteActions(status || undefined);
  const count = (s: RemoteAction["status"]) =>
    all.isPending ? loading : all.data ? valueOf(String(all.data.items.filter((a) => a.status === s).length)) : failed(all.error?.message);

  const columns: GridColDef<RemoteAction>[] = [
    { field: "sequence", headerName: "Message", width: 100, renderCell: ({ row }) => <span className="font-mono">#{row.sequence}</span> },
    { field: "operationId", headerName: "Proposal", minWidth: 200, flex: 1.8, sortable: false, renderCell: ({ row }) => <ProposalLink action={row} /> },
    { field: "target", headerName: "Program", minWidth: 150, flex: 0.8, sortable: false, renderCell: ({ row }) => <ChainValue kind="address" value={row.target} /> },
    { field: "status", headerName: "Status", width: 120, renderCell: ({ row }) => <StateBadge state={row.status} /> },
    {
      field: "executableAt",
      headerName: "Executable",
      minWidth: 170,
      flex: 1,
      renderCell: ({ row }) => (row.status === "pending" ? relativeToNow(row.executableAt) : formatTime(row.executableAt)),
    },
    { field: "receivedTx", headerName: "Received", minWidth: 150, sortable: false, renderCell: ({ row }) => <ChainValue kind="tx" value={row.receivedTx} /> },
  ];

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Received governance"
        description="Decisions passed on Arbitrum, delivered through Wormhole, and held here until their delay has passed. The guardian can cancel one while it waits."
      />
      <div className="grid gap-3 sm:grid-cols-3">
        <StatCard label="Waiting" icon={Clock} state={count("pending")} />
        <StatCard label="Executed" icon={CheckCircle2} state={count("executed")} />
        <StatCard label="Cancelled" icon={XCircle} state={count("cancelled")} />
      </div>
      <Panel
        title="Actions"
        actions={
          <ToggleGroup type="single" size="sm" value={status} onValueChange={(v) => setStatus(v)} aria-label="Filter by status">
            <ToggleGroupItem value="" className="px-2.5 text-xs">All</ToggleGroupItem>
            <ToggleGroupItem value="pending" className="px-2.5 text-xs">Waiting</ToggleGroupItem>
            <ToggleGroupItem value="executed" className="px-2.5 text-xs">Executed</ToggleGroupItem>
            <ToggleGroupItem value="cancelled" className="px-2.5 text-xs">Cancelled</ToggleGroupItem>
          </ToggleGroup>
        }
      >
        <Async query={{ ...actions, data: actions.data?.items }} empty="No governance action matches.">
          {(rows) => <DataTable rows={rows} columns={columns} getRowId={(r) => `${r.emitterChain}-${r.sequence}`} rowHeight={48} />}
        </Async>
      </Panel>
    </div>
  );
}

// The proposal an action came from, found by its operation id on its source chain, when that chain is
// served here. The source is an EVM chain, so its numeric id is exact.
function ProposalLink({ action }: { action: RemoteAction }) {
  const served = useServedChains();
  const source = chainById(String(action.sourceChainId));
  const onServed = source && served.some((c) => c.name === source.name) ? source : undefined;
  const proposals = useProposals(onServed ? onServed.name : null);
  const match = onServed ? proposals.data?.items.find((p) => p.operationId === action.operationId) : undefined;

  if (!onServed || !match) {
    return <span className="font-mono text-muted-foreground">operation #{action.operationId}</span>;
  }
  return (
    <Link href={withChain(`/governance/${match.proposalId}`, onServed)} className="inline-flex min-w-0 items-center gap-1 text-foreground hover:text-primary hover:underline">
      <span className="truncate">{match.title || `Proposal ${match.proposalId}`}</span>
      <ArrowUpRight className="size-3.5 shrink-0" aria-hidden="true" />
    </Link>
  );
}
