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
                    <span title="Recorded, but carries no penalty today — see the note below.">
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

      {/* docs/v1.1-frontend-plan.md §2.4. A count that implies an enforcement which does not exist
          tells an operator something untrue, so the absence is stated rather than left inferred. */}
      <p className="rounded border border-zinc-800 bg-zinc-900/60 px-4 py-2 text-sm text-zinc-400">
        Missed rounds are recorded but not penalised. Slashing is implemented for outlier
        submissions and invalid signatures only; there is no missed-round penalty in this release.
      </p>
    </div>
  );
}
