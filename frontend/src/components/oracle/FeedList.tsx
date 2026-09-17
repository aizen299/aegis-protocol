"use client";

import { ArrowUpRight, Radio } from "lucide-react";
import Link from "next/link";
import { formatUnits } from "viem";

import { Async } from "@/components/Async";
import { Sparkline } from "@/components/common/Sparkline";
import { PageHeader } from "@/components/shell/PageHeader";
import { StateBadge } from "@/components/StateBadge";
import { Card } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import type { OracleFeed } from "@/lib/api";
import { useChainHref } from "@/lib/chainContext";
import { relativeToNow, shortId } from "@/lib/format";
import { useOracleFeeds, useOracleRounds } from "@/lib/queries";
import { formatRaw } from "@/lib/units";

export function FeedList() {
  const feeds = useOracleFeeds();

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Oracle feeds"
        description="Values medianised from staked nodes' signed submissions, one round at a time."
      />
      <Async query={{ ...feeds, data: feeds.data?.items }} empty="No feeds are registered.">
        {(items) => (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
            {items.map((feed) => (
              <FeedCard key={feed.feedId} feed={feed} />
            ))}
          </div>
        )}
      </Async>
    </div>
  );
}

function FeedCard({ feed }: { feed: OracleFeed }) {
  const chainHref = useChainHref();
  const rounds = useOracleRounds(feed.feedId);
  const settled = (rounds.data?.items ?? []).filter((r) => r.state.toLowerCase() === "settled");
  const latest = settled[0];
  const history = [...settled].reverse().map((r) => Number(formatUnits(BigInt(r.aggregatedValue), r.decimals)));

  return (
    <Link
      href={chainHref(`/oracle/feeds/${feed.feedId}`)}
      className="group rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <Card className="flex h-full flex-col gap-3 p-4 shadow-none transition-colors duration-150 group-hover:border-primary/40">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <p className="flex items-center gap-2 font-display font-semibold">
              <Radio className="size-4 text-muted-foreground" aria-hidden="true" />
              <span className="truncate">{feed.name}</span>
            </p>
            <p className="mt-0.5 font-mono text-[11px] text-muted-foreground">{shortId(feed.feedId)}</p>
          </div>
          <div className="flex items-center gap-1.5">
            <StateBadge state={feed.active ? "active" : "inactive"} />
            <ArrowUpRight className="size-4 text-muted-foreground transition-transform group-hover:-translate-y-0.5 group-hover:translate-x-0.5" aria-hidden="true" />
          </div>
        </div>
        {rounds.isPending ? (
          <Skeleton className="h-16 w-full" />
        ) : (
          <>
            <div className="flex items-baseline justify-between gap-2">
              <span className="tabular font-display text-2xl font-semibold tracking-tight">
                {latest ? formatRaw(latest.aggregatedValue, latest.decimals) ?? "unavailable" : "—"}
              </span>
              <span className="text-xs text-muted-foreground">
                {latest ? `settled ${relativeToNow(latest.settledAt)}` : rounds.isError ? "rounds unavailable" : "no settled round"}
              </span>
            </div>
            <Sparkline values={history} label={`${feed.name} history`} />
          </>
        )}
      </Card>
    </Link>
  );
}
