"use client";

import type { GridColDef } from "@mui/x-data-grid";
import { ArrowRightLeft } from "lucide-react";
import Link from "next/link";

import { Async } from "@/components/Async";
import { ChainValue } from "@/components/common/Address";
import { DataTable } from "@/components/common/DataTable";
import { Panel } from "@/components/Panel";
import { PageHeader } from "@/components/shell/PageHeader";
import { StateBadge } from "@/components/StateBadge";
import type { Proposal } from "@/lib/api";
import { useChainHref, useServedChains } from "@/lib/chainContext";
import { relativeToNow } from "@/lib/format";
import { isRemote } from "@/lib/governanceTimeline";
import { useProposals } from "@/lib/queries";
import { formatRaw } from "@/lib/units";
import { chainForNumericId } from "./remoteChain";

export function ProposalList() {
  const chainHref = useChainHref();
  const served = useServedChains();
  const proposals = useProposals();

  const columns: GridColDef<Proposal>[] = [
    {
      field: "title",
      headerName: "Proposal",
      flex: 2,
      minWidth: 200,
      renderCell: ({ row }) => (
        <Link className="truncate font-medium text-foreground hover:text-primary hover:underline" href={chainHref(`/governance/${row.proposalId}`)}>
          {row.title || row.proposalId}
        </Link>
      ),
    },
    {
      field: "state",
      headerName: "State",
      minWidth: 200,
      flex: 1.1,
      renderCell: ({ row }) => (
        <span className="flex items-center gap-1.5">
          <StateBadge state={row.state} />
          {row.remote ? (
            <span className="inline-flex items-center gap-1" title={`On ${chainForNumericId(row.remote.chainId, served)?.label ?? "the destination"}`}>
              <ArrowRightLeft className="size-3 text-muted-foreground" aria-label="remote" />
              <StateBadge state={row.remote.status} />
            </span>
          ) : isRemote(row) && row.dispatchedAt ? (
            <span className="text-[11px] text-muted-foreground">awaiting relay</span>
          ) : null}
        </span>
      ),
    },
    { field: "votesFor", headerName: "For", flex: 0.8, minWidth: 90, sortable: false, renderCell: ({ row }) => <span className="tabular font-mono">{formatRaw(row.votesFor, row.voteDecimals) ?? "unavailable"}</span> },
    { field: "votesAgainst", headerName: "Against", flex: 0.8, minWidth: 90, sortable: false, renderCell: ({ row }) => <span className="tabular font-mono">{formatRaw(row.votesAgainst, row.voteDecimals) ?? "unavailable"}</span> },
    { field: "proposer", headerName: "Proposer", minWidth: 150, sortable: false, renderCell: ({ row }) => <ChainValue kind="address" value={row.proposer} /> },
    { field: "voteEnd", headerName: "Voting ends", minWidth: 120, flex: 0.9, renderCell: ({ row }) => relativeToNow(new Date(row.voteEnd * 1000).toISOString()) },
  ];

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title="Governance" description="Proposals voted with the protocol's token, held by a timelock, and executed here or dispatched to another chain." />
      <Panel title="Proposals">
        <Async query={{ ...proposals, data: proposals.data?.items }} empty="No proposals have been created.">
          {(rows) => <DataTable rows={rows} columns={columns} getRowId={(r) => r.proposalId} rowHeight={52} />}
        </Async>
      </Panel>
    </div>
  );
}
