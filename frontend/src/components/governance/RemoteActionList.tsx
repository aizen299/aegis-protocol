"use client";

import { Async } from "@/components/Async";
import { Panel } from "@/components/Panel";
import { PageHeader } from "@/components/shell/PageHeader";
import { StateBadge } from "@/components/StateBadge";
import { Cell, Row, Table } from "@/components/Table";
import { formatTime, shortAddress } from "@/lib/format";
import { useRemoteActions } from "@/lib/queries";

export function RemoteActionList() {
  const actions = useRemoteActions();

  return (
    <>
      <PageHeader
        title="Received governance"
        description="Decisions passed on Arbitrum, delivered through Wormhole, and held here until their delay has passed."
      />
      <Panel title="Actions">
        <Async query={{ ...actions, data: actions.data?.items }} empty="No governance action has been received on this chain.">
          {(items) => (
            <Table headers={["Sequence", "Operation", "Target", "Status", "Executable at"]}>
              {items.map((a) => (
                <Row key={`${a.emitterChain}-${a.sequence}`}>
                  <Cell mono>{a.sequence}</Cell>
                  <Cell mono>#{a.operationId}</Cell>
                  <Cell mono>{shortAddress(a.target)}</Cell>
                  <Cell>
                    <StateBadge state={a.status} />
                  </Cell>
                  <Cell>{formatTime(a.executableAt)}</Cell>
                </Row>
              ))}
            </Table>
          )}
        </Async>
      </Panel>
    </>
  );
}
