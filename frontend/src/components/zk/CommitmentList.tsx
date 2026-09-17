"use client";

import { Async } from "@/components/Async";
import { Panel } from "@/components/Panel";
import { Cell, Row, Table } from "@/components/Table";
import { formatTime, shortId } from "@/lib/format";
import { useCommitments } from "@/lib/queries";

export function CommitmentList() {
  const commitments = useCommitments();

  return (
    <div className="flex flex-col gap-5">
      <Panel title="Commitments">
        <Async
          query={{ ...commitments, data: commitments.data?.items }}
          empty="No commitments have been inserted."
        >
          {(items) => (
            <Table headers={["Leaf", "Commitment", "Root after", "Inserted"]}>
              {items.map((c) => (
                <Row key={`${c.txHash}:${c.logIndex}`}>
                  <Cell mono>{c.leafIndex}</Cell>
                  <Cell mono>{shortId(c.commitment)}</Cell>
                  <Cell mono>{shortId(c.rootAfter)}</Cell>
                  <Cell>{formatTime(c.insertedAt)}</Cell>
                </Row>
              ))}
            </Table>
          )}
        </Async>
      </Panel>

      <p className="rounded border border-border bg-muted/60 px-4 py-2 text-sm text-muted-foreground">
        There is no depositor column, and that is the design rather than an omission. A table joining
        a commitment to the account that inserted it would turn correlating deposits with spends from
        log archaeology into a single lookup.
      </p>
    </div>
  );
}
