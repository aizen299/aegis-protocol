"use client";

import type { GridColDef } from "@mui/x-data-grid";

import { Async } from "@/components/Async";
import { ChainValue } from "@/components/common/Address";
import { DataTable } from "@/components/common/DataTable";
import { Panel } from "@/components/Panel";
import type { OracleSubmission } from "@/lib/api";
import { formatTime } from "@/lib/format";
import { useOracleSubmissions } from "@/lib/queries";
import { formatRaw } from "@/lib/units";

const columns: GridColDef<OracleSubmission>[] = [
  { field: "node", headerName: "Node", flex: 1.2, minWidth: 170, sortable: false, renderCell: ({ row }) => <ChainValue kind="address" value={row.node} /> },
  {
    field: "value",
    headerName: "Value",
    flex: 1,
    minWidth: 120,
    sortComparator: (a: string, b: string) => (BigInt(a) < BigInt(b) ? -1 : BigInt(a) > BigInt(b) ? 1 : 0),
    renderCell: ({ row }) => <span className="tabular font-mono">{formatRaw(row.value, row.decimals) ?? "unavailable"}</span>,
  },
  {
    field: "isOutlier",
    headerName: "Outlier",
    width: 100,
    renderCell: ({ row }) => (row.isOutlier ? <span className="font-medium text-warning">outlier</span> : <span className="text-muted-foreground">—</span>),
  },
  {
    field: "nonceKnown",
    headerName: "Signature",
    width: 130,
    // Unverifiable is not the same as invalid: a submission indexed before the nonce was emitted cannot
    // be checked, and slashing for it would punish a node for an indexing gap.
    renderCell: ({ row }) =>
      row.nonceKnown ? (
        <span className="text-muted-foreground">verifiable</span>
      ) : (
        <span className="text-muted-foreground" title="indexed before the nonce was emitted">
          unverifiable
        </span>
      ),
  },
  { field: "submittedAt", headerName: "Submitted", flex: 1, minWidth: 170, renderCell: ({ row }) => formatTime(row.submittedAt) },
];

export function SubmissionList({ roundId }: { roundId: string }) {
  const submissions = useOracleSubmissions(roundId);

  return (
    <Panel title="Submissions">
      <Async query={{ ...submissions, data: submissions.data?.items }} empty="No node submitted to this round.">
        {(rows) => <DataTable rows={rows} columns={columns} getRowId={(r) => `${r.txHash}:${r.logIndex}`} />}
      </Async>
    </Panel>
  );
}
