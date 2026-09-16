"use client";

import { Async } from "@/components/Async";
import { Panel } from "@/components/Panel";
import { Cell, Row, Table } from "@/components/Table";
import { shortId } from "@/lib/format";
import { useZkActions } from "@/lib/queries";

export function ActionList() {
  const actions = useZkActions();

  return (
    <Panel title="Registered actions">
      <Async query={{ ...actions, data: actions.data?.items }} empty="No actions are registered.">
        {(items) => (
          <Table headers={["Name", "Action id", "Status"]}>
            {items.map((action) => (
              <Row key={action.actionId}>
                <Cell>{action.name}</Cell>
                <Cell mono>{shortId(action.actionId)}</Cell>
                <Cell>
                  {action.registered ? (
                    "registered"
                  ) : (
                    <span className="text-zinc-500" title="No new proof can be presented for it">
                      deregistered
                    </span>
                  )}
                </Cell>
              </Row>
            ))}
          </Table>
        )}
      </Async>
    </Panel>
  );
}
