"use client";

import { useEffect, useState } from "react";

import { ChainValue } from "@/components/common/Address";
import { Timeline } from "@/components/ui/timeline";
import type { Proposal } from "@/lib/api";
import { useChain, useServedChains } from "@/lib/chainContext";
import { formatTime, relativeToNow } from "@/lib/format";
import { governanceTimeline } from "@/lib/governanceTimeline";
import { chainForNumericId } from "./remoteChain";

export function CrossChainTimeline({ proposal }: { proposal: Proposal }) {
  const chain = useChain();
  const served = useServedChains();
  const remoteChain = chainForNumericId(proposal.remote?.chainId ?? proposal.action.targetChainId, served);
  const [now, setNow] = useState(() => Date.now());

  // Ticks only while something is counting down.
  const waiting = proposal.remote?.status === "pending" && Date.parse(proposal.remote.executableAt) > now;
  useEffect(() => {
    if (!waiting) return;
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [waiting]);

  const steps = governanceTimeline(proposal, now, remoteChain?.label ?? "the destination");

  return (
    <Timeline
      items={steps.map((s) => ({
        id: s.id,
        title: s.title,
        status: s.status,
        timestamp: s.at ? (s.status === "active" || s.status === "pending" ? relativeToNow(s.at, now) : formatTime(s.at)) : undefined,
        description: s.detail,
        content: s.tx ? <ChainValue kind="tx" value={s.tx.hash} chain={s.tx.remote ? remoteChain : chain} /> : undefined,
      }))}
    />
  );
}
