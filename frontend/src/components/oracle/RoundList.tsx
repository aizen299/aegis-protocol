"use client";

import Link from "next/link";

import { Async } from "@/components/Async";
import { Panel } from "@/components/Panel";
import { StateBadge } from "@/components/StateBadge";
import { Cell, Row, Table } from "@/components/Table";
import { useChainHref } from "@/lib/chainContext";
import { formatTime } from "@/lib/format";
import { useOracleRounds } from "@/lib/queries";
import { formatRaw } from "@/lib/units";

export function RoundList({ feedId }: { feedId: string }) {
  const chainHref = useChainHref();
  const rounds = useOracleRounds(feedId);

  return (
    <Panel title="Rounds">
      <Async query={{ ...rounds, data: rounds.data?.items }} empty="This feed has no rounds yet.">
        {(items) => (
          <Table headers={["Round", "State", "Value", "Submissions", "Opened", "Settled"]}>
            {items.map((round) => (
              <Row key={round.roundId}>
                <Cell mono>
                  <Link
                    className="text-info hover:underline"
                    href={chainHref(`/oracle/rounds/${round.roundId}`)}
                  >
                    {round.roundId}
                  </Link>
                </Cell>
                <Cell>
                  <StateBadge state={round.state} />
                </Cell>
                <Cell mono>
                  {round.state.toLowerCase() === "settled"
                    ? (formatRaw(round.aggregatedValue, round.decimals) ?? "unavailable")
                    : "—"}
                </Cell>
                <Cell mono>
                  {round.submissionCount} / {round.eligibleCount}
                </Cell>
                <Cell>{formatTime(round.openedAt)}</Cell>
                <Cell>{formatTime(round.settledAt)}</Cell>
              </Row>
            ))}
          </Table>
        )}
      </Async>
    </Panel>
  );
}
