"use client";

import { Async } from "@/components/Async";
import { Panel, Stat } from "@/components/Panel";
import { StateBadge } from "@/components/StateBadge";
import { SubmissionList } from "@/components/oracle/SubmissionList";
import { formatTime } from "@/lib/format";
import { useOracleRound } from "@/lib/queries";
import { failed, value as valueOf } from "@/lib/readState";
import { formatRaw } from "@/lib/units";

export function RoundDetail({ roundId }: { roundId: string }) {
  const round = useOracleRound(roundId);

  return (
    <div className="flex flex-col gap-5">
      <Panel title={`Round ${roundId}`}>
        <Async query={round} empty="This round does not exist.">
          {(data) => {
            const settled = data.state.toLowerCase() === "settled";
            const aggregated = formatRaw(data.aggregatedValue, data.decimals);

            return (
              <>
                <div className="mb-3">
                  <StateBadge state={data.state} />
                </div>
                <Stat
                  label="Aggregated value"
                  state={
                    settled
                      ? aggregated === undefined
                        ? failed("the value could not be read")
                        : valueOf(aggregated)
                      : valueOf("not settled")
                  }
                />
                <Stat label="Feed" state={valueOf(data.feedName)} />
                <Stat
                  label="Submissions"
                  state={valueOf(`${data.submissionCount} of ${data.eligibleCount} eligible`)}
                />
                <Stat label="Opened" state={valueOf(formatTime(data.openedAt))} />
                <Stat label="Deadline" state={valueOf(formatTime(data.deadline))} />
                <Stat label="Settled" state={valueOf(formatTime(data.settledAt))} />
              </>
            );
          }}
        </Async>
      </Panel>

      <SubmissionList roundId={roundId} />
    </div>
  );
}
