"use client";

import type { GridColDef } from "@mui/x-data-grid";

import { Async } from "@/components/Async";
import { ChainValue } from "@/components/common/Address";
import { DataTable } from "@/components/common/DataTable";
import { CrossChainTimeline } from "@/components/governance/CrossChainTimeline";
import { ProposalActions } from "@/components/governance/ProposalActions";
import { Panel, Stat } from "@/components/Panel";
import { PageHeader } from "@/components/shell/PageHeader";
import { StateBadge } from "@/components/StateBadge";
import type { Proposal, Vote } from "@/lib/api";
import { useServedChains } from "@/lib/chainContext";
import { formatTime, formatUnixTime } from "@/lib/format";
import { useProposal, useProposalVotes } from "@/lib/queries";
import { value as valueOf } from "@/lib/readState";
import { formatRaw } from "@/lib/units";
import { chainForNumericId } from "./remoteChain";

const supportLabels = ["against", "for", "abstain"];

export function ProposalDetail({ proposalId }: { proposalId: string }) {
  const proposal = useProposal(proposalId);

  return (
    <div className="flex flex-col gap-6">
      <Async query={proposal} empty="This proposal does not exist.">
        {(data) => (
          <>
            <PageHeader
              title={data.title || `Proposal ${proposalId}`}
              description={`Proposal #${proposalId}, voting ${formatUnixTime(data.voteStart)} to ${formatUnixTime(data.voteEnd)}`}
              actions={<StateBadge state={data.state} />}
            />
            <div className="grid gap-4 lg:grid-cols-[1.4fr_1fr]">
              <div className="flex min-w-0 flex-col gap-4">
                {data.description ? (
                  <Panel title="Description">
                    <p className="whitespace-pre-wrap text-sm leading-relaxed text-pretty">{data.description}</p>
                  </Panel>
                ) : null}
                <Tally proposal={data} />
                <ProposalActions proposalId={proposalId} voteStart={data.voteStart} />
                <ActionPanel proposal={data} />
              </div>
              <div className="flex min-w-0 flex-col gap-4">
                <Panel title="Progress">
                  <CrossChainTimeline proposal={data} />
                </Panel>
                <Panel title="Details">
                  <div className="flex items-baseline justify-between gap-4 border-b py-2.5">
                    <span className="text-sm text-muted-foreground">Proposer</span>
                    <ChainValue kind="address" value={data.proposer} />
                  </div>
                  <Stat label="Operation" state={valueOf(data.operationId ? `#${data.operationId}` : "not queued")} />
                  <Stat label="Executable at" state={valueOf(formatTime(data.executableAt))} />
                  <Stat label="Executed" state={valueOf(formatTime(data.executedAt))} />
                  <Stat label="Cancelled" state={valueOf(formatTime(data.cancelledAt))} />
                </Panel>
              </div>
            </div>
          </>
        )}
      </Async>

      <VoteList proposalId={proposalId} />
    </div>
  );
}

function Tally({ proposal }: { proposal: Proposal }) {
  const parts = [
    { key: "for", label: "For", raw: proposal.votesFor, bar: "bg-success" },
    { key: "against", label: "Against", raw: proposal.votesAgainst, bar: "bg-destructive" },
    { key: "abstain", label: "Abstain", raw: proposal.votesAbstain, bar: "bg-muted-foreground" },
  ];
  let total = 0n;
  const values = parts.map((p) => {
    try {
      const v = BigInt(p.raw);
      total += v;
      return v;
    } catch {
      return undefined;
    }
  });

  return (
    <Panel title="Tally">
      <div className="flex flex-col gap-3">
        {parts.map((p, i) => {
          const v = values[i];
          const share = v === undefined || total === 0n ? 0 : Number((v * 10_000n) / total) / 100;
          const text = formatRaw(p.raw, proposal.voteDecimals);
          return (
            <div key={p.key} className="flex flex-col gap-1.5">
              <div className="flex items-baseline justify-between gap-3 text-sm">
                <span>{p.label}</span>
                <span className="tabular font-mono" data-testid={`tally-${p.key}`}>
                  {text ?? "unavailable"} <span className="text-muted-foreground">{total > 0n ? `${share.toFixed(1)}%` : ""}</span>
                </span>
              </div>
              <div
                className="h-2 overflow-hidden rounded-full bg-muted"
                role="meter"
                aria-label={`${p.label} share of votes`}
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={share}
              >
                <div className={`h-full rounded-full ${p.bar} transition-[width] duration-500`} style={{ width: `${share}%` }} />
              </div>
            </div>
          );
        })}
      </div>
    </Panel>
  );
}

function ActionPanel({ proposal }: { proposal: Proposal }) {
  const served = useServedChains();
  const target = chainForNumericId(proposal.action.targetChainId, served);
  return (
    <Panel title="Action">
      {/* Shown in full, including calldata. A proposal a voter cannot read is one they are trusting
          rather than judging. */}
      <Stat label="Target chain" state={valueOf(target?.label ?? String(proposal.action.targetChainId))} />
      <div className="flex flex-col gap-1 border-b py-2.5 sm:flex-row sm:items-baseline sm:justify-between sm:gap-4">
        <span className="text-sm text-muted-foreground">Target</span>
        <span className="break-all font-mono text-sm sm:text-right">{proposal.action.target}</span>
      </div>
      <Stat label="Value" state={valueOf(proposal.action.value)} />
      <p className="mt-3 break-all rounded-md bg-muted/50 p-3 font-mono text-xs text-muted-foreground">{proposal.action.calldata}</p>
    </Panel>
  );
}

const voteColumns: GridColDef<Vote>[] = [
  { field: "voter", headerName: "Voter", minWidth: 170, flex: 1, sortable: false, renderCell: ({ row }) => <ChainValue kind="address" value={row.voter} /> },
  { field: "support", headerName: "Support", width: 110, renderCell: ({ row }) => supportLabels[row.support] ?? `unknown (${row.support})` },
  {
    field: "weight",
    headerName: "Weight",
    minWidth: 130,
    flex: 1,
    sortComparator: (a: string, b: string) => (BigInt(a) < BigInt(b) ? -1 : BigInt(a) > BigInt(b) ? 1 : 0),
    renderCell: ({ row }) => <span className="tabular font-mono">{formatRaw(row.weight, row.voteDecimals) ?? "unavailable"}</span>,
  },
  { field: "reason", headerName: "Reason", flex: 1.5, minWidth: 160, sortable: false, renderCell: ({ row }) => row.reason || "—" },
  { field: "votedAt", headerName: "Cast", minWidth: 170, flex: 1, renderCell: ({ row }) => formatTime(row.votedAt) },
];

function VoteList({ proposalId }: { proposalId: string }) {
  const votes = useProposalVotes(proposalId);
  return (
    <Panel title="Votes">
      <Async query={{ ...votes, data: votes.data?.items }} empty="Nobody has voted yet.">
        {(rows) => <DataTable rows={rows} columns={voteColumns} getRowId={(r) => `${r.txHash}:${r.logIndex}`} />}
      </Async>
    </Panel>
  );
}
