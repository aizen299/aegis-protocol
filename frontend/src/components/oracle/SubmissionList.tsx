"use client";

import { Async } from "@/components/Async";
import { Panel } from "@/components/Panel";
import { Cell, Row, Table } from "@/components/Table";
import { formatTime, shortAddress } from "@/lib/format";
import { useOracleSubmissions } from "@/lib/queries";
import { formatRaw } from "@/lib/units";

export function SubmissionList({ roundId }: { roundId: string }) {
  const submissions = useOracleSubmissions(roundId);

  return (
    <Panel title="Submissions">
      <Async
        query={{ ...submissions, data: submissions.data?.items }}
        empty="No node submitted to this round."
      >
        {(items) => (
          <Table headers={["Node", "Value", "Outlier", "Signature", "Submitted"]}>
            {items.map((submission) => (
              <Row key={`${submission.txHash}:${submission.logIndex}`}>
                <Cell mono>{shortAddress(submission.node)}</Cell>
                <Cell mono>
                  {formatRaw(submission.value, submission.decimals) ?? "unavailable"}
                </Cell>
                <Cell>
                  {submission.isOutlier ? (
                    <span className="text-warning">outlier</span>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </Cell>
                <Cell>
                  {/* Unverifiable is not the same as invalid: a submission indexed before the
                      nonce was emitted cannot be checked, and slashing for it would punish a node
                      for an indexing gap. */}
                  {submission.nonceKnown ? (
                    <span className="text-muted-foreground">verifiable</span>
                  ) : (
                    <span className="text-muted-foreground" title="indexed before the nonce was emitted">
                      unverifiable
                    </span>
                  )}
                </Cell>
                <Cell>{formatTime(submission.submittedAt)}</Cell>
              </Row>
            ))}
          </Table>
        )}
      </Async>
    </Panel>
  );
}
