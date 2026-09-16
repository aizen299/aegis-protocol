"use client";

import { Async } from "@/components/Async";
import { Panel } from "@/components/Panel";
import { Cell, Row, Table } from "@/components/Table";
import { shortAddress } from "@/lib/format";
import { useOracleNodes } from "@/lib/queries";
import { formatRaw } from "@/lib/units";

export function NodeList() {
  const nodes = useOracleNodes();

  return (
    <div className="flex flex-col gap-5">
      <Panel title="Oracle nodes">
        <Async query={{ ...nodes, data: nodes.data?.items }} empty="No nodes are registered.">
          {(items) => (
            <Table headers={["Node", "Staked", "Slashed", "Missed rounds", "Status"]}>
              {items.map((node) => (
                <Row key={node.address}>
                  <Cell mono>{shortAddress(node.address)}</Cell>
                  <Cell mono>
                    {formatRaw(node.stakedAmount, node.decimals) ?? "unavailable"}
                  </Cell>
                  <Cell mono>{formatRaw(node.slashedTotal, node.decimals) ?? "unavailable"}</Cell>
                  <Cell mono>
                    <span title="Slashable misses only — see the note below for what is excused.">
                      {node.missedRounds}
                    </span>
                  </Cell>
                  <Cell>{node.active ? "active" : "inactive"}</Cell>
                </Row>
              ))}
            </Table>
          )}
        </Async>
      </Panel>

      {/* The note this replaced said misses were "recorded but not penalised". The second half was
          true and the first was not: nothing wrote the count, so every node showed zero. The rules
          are stated here so an operator can predict their own penalty. See
          docs/v1.3-missed-round-slashing-plan.md. */}
      <div
        className="rounded border border-zinc-800 bg-zinc-900/60 px-4 py-2 text-sm text-zinc-400"
        data-testid="missed-round-rules"
      >
        <p>
          A missed round costs 0.5% of stake. A third consecutive miss costs 10% instead, and the
          count starts again.
        </p>
        <p className="mt-1">
          Not counted as a miss: a round nobody submitted to, a round settled before its deadline,
          and a round the node was removed from by an admin or for low stake before its deadline.
          Requesting to unstake is not an excuse.
        </p>
      </div>
    </div>
  );
}
