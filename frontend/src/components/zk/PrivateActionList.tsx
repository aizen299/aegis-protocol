"use client";

import { Async } from "@/components/Async";
import { Panel } from "@/components/Panel";
import { Cell, Row, Table } from "@/components/Table";
import { formatTime, shortId } from "@/lib/format";
import { usePrivateActions } from "@/lib/queries";

export function PrivateActionList() {
  const actions = usePrivateActions();

  return (
    <Panel title="Executed private actions">
      <Async
        query={{ ...actions, data: actions.data?.items }}
        empty="No private action has been executed."
      >
        {(items) => (
          <Table headers={["Nullifier", "Action", "Root proved against", "Executed"]}>
            {items.map((a) => (
              <Row key={`${a.txHash}:${a.logIndex}`}>
                <Cell mono>{shortId(a.nullifier)}</Cell>
                <Cell mono>{shortId(a.actionId)}</Cell>
                <Cell mono>{shortId(a.root)}</Cell>
                <Cell>{formatTime(a.executedAt)}</Cell>
              </Row>
            ))}
          </Table>
        )}
      </Async>
    </Panel>
  );
}
