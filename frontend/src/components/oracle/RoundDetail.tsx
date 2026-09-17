"use client";

import { Activity, CalendarClock, Users } from "lucide-react";

import { Async } from "@/components/Async";
import { ChainValue } from "@/components/common/Address";
import { StatCard } from "@/components/common/StatCard";
import { SubmissionList } from "@/components/oracle/SubmissionList";
import { Panel, Stat } from "@/components/Panel";
import { PageHeader } from "@/components/shell/PageHeader";
import { StateBadge } from "@/components/StateBadge";
import { formatTime } from "@/lib/format";
import { useOracleRound } from "@/lib/queries";
import { failed, value as valueOf } from "@/lib/readState";
import { formatRaw } from "@/lib/units";

export function RoundDetail({ roundId }: { roundId: string }) {
  const round = useOracleRound(roundId);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader title={`Round #${roundId}`} description={round.data ? round.data.feedName : undefined} actions={round.data ? <StateBadge state={round.data.state} /> : null} />
      <Async query={round} empty="This round does not exist.">
        {(data) => {
          const settled = data.state.toLowerCase() === "settled";
          const aggregated = formatRaw(data.aggregatedValue, data.decimals);

          return (
            <>
              <div className="grid gap-3 sm:grid-cols-3">
                <StatCard
                  label="Aggregated value"
                  icon={Activity}
                  state={settled ? (aggregated === undefined ? failed("the value could not be read") : valueOf(aggregated)) : valueOf("not settled")}
                />
                <StatCard label="Submissions" icon={Users} state={valueOf(`${data.submissionCount} of ${data.eligibleCount}`)} hint="eligible nodes" />
                <StatCard label="Deadline" icon={CalendarClock} state={valueOf(formatTime(data.deadline))} />
              </div>
              <Panel title="Round">
                <Stat label="Feed" state={valueOf(data.feedName)} />
                <Stat label="Opened" state={valueOf(formatTime(data.openedAt))} />
                <Stat label="Settled" state={valueOf(formatTime(data.settledAt))} />
                <Stat label="Node set version" state={valueOf(data.nodeSetVersion)} />
                <div className="flex items-baseline justify-between gap-4 py-2.5">
                  <span className="text-sm text-muted-foreground">Last transaction</span>
                  <ChainValue kind="tx" value={data.txHash} />
                </div>
              </Panel>
            </>
          );
        }}
      </Async>

      <SubmissionList roundId={roundId} />
    </div>
  );
}
