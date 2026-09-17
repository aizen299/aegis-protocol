import type { Proposal } from "./api";
import type { TimelineStatus } from "@/components/ui/timeline";

// Where a proposal stands, step by step, on its own chain and, once dispatched, on the chain that
// receives it. Pure, so every state and remote status is testable. docs/v2.0-solana-plan.md §21.5.

export type Step = {
  id: string;
  title: string;
  status: TimelineStatus;
  at?: string;
  detail?: string;
  tx?: { hash: string; remote: boolean };
};

const terminalFailure = new Set(["defeated", "cancelled", "failed", "expired"]);

export function isRemote(p: Proposal): boolean {
  return Boolean(p.dispatchedAt || p.remote || p.state.toLowerCase() === "dispatched" || p.action.targetChainId !== p.chainId);
}

export function governanceTimeline(p: Proposal, now: number, remoteLabel: string): Step[] {
  const state = p.state.toLowerCase();
  const nowSec = Math.floor(now / 1000);
  const votingOver = nowSec >= p.voteEnd;
  const passed = Boolean(p.queuedAt) || ["succeeded", "queued", "dispatched", "executed"].includes(state);

  const steps: Step[] = [
    { id: "proposed", title: "Proposed", status: "completed", at: iso(p.voteStart), detail: "Voting weight is read at the snapshot.", tx: { hash: p.txHash, remote: false } },
    {
      id: "voting",
      title: "Voting",
      status: state === "defeated" ? "error" : passed ? "completed" : state === "pending" ? "pending" : state === "cancelled" && !votingOver ? "error" : votingOver ? "completed" : "active",
      at: iso(p.voteEnd),
      detail: state === "defeated" ? "Defeated: quorum or majority was not reached." : votingOver ? "Voting closed." : state === "pending" ? "Voting has not opened." : "Voting is open.",
    },
    {
      id: "queued",
      title: "Queued in the timelock",
      status: p.queuedAt ? "completed" : terminalFailure.has(state) ? "skipped" : "pending",
      at: p.queuedAt,
      detail: p.queuedAt ? "Waits out the timelock delay before it can execute." : undefined,
    },
  ];

  if (!isRemote(p)) {
    steps.push({
      id: "executed",
      title: "Executed",
      status: p.executedAt ? "completed" : p.cancelledAt ? "error" : p.queuedAt && p.executableAt && Date.parse(p.executableAt) <= now ? "active" : terminalFailure.has(state) ? "skipped" : "pending",
      at: p.executedAt ?? p.cancelledAt,
      detail: p.cancelledAt ? "Cancelled before execution." : p.executedAt ? undefined : p.queuedAt ? "Anyone may execute once the delay has passed." : undefined,
    });
    return steps;
  }

  const r = p.remote;
  steps.push({
    id: "dispatched",
    title: "Dispatched through Wormhole",
    status: p.dispatchedAt ? "completed" : p.cancelledAt ? "error" : terminalFailure.has(state) ? "skipped" : "pending",
    at: p.dispatchedAt,
    detail: p.dispatchedAt ? "Published for the guardians to sign. Nothing ran on this chain." : undefined,
  });
  steps.push({
    id: "received",
    title: `Received on ${remoteLabel}`,
    status: r ? "completed" : p.dispatchedAt ? "active" : terminalFailure.has(state) ? "skipped" : "pending",
    at: r?.receivedAt,
    detail: r ? `Wormhole message ${r.sequence}.` : p.dispatchedAt ? "Waiting for the signed message to be relayed." : undefined,
    tx: r ? { hash: r.receivedTx, remote: true } : undefined,
  });

  const delayOver = r ? Date.parse(r.executableAt) <= now : false;
  steps.push({
    id: "delay",
    title: `Delay on ${remoteLabel}`,
    status: !r ? (terminalFailure.has(state) ? "skipped" : "pending") : r.status === "pending" ? (delayOver ? "completed" : "active") : r.status === "cancelled" && !delayOver ? "error" : "completed",
    at: r?.executableAt,
    detail: !r ? undefined : r.status !== "pending" ? "The delay ran its course." : delayOver ? "The delay has passed." : "The guardian can still cancel during the delay.",
  });
  steps.push({
    id: "remote-outcome",
    title: r?.status === "cancelled" ? `Cancelled on ${remoteLabel}` : `Executed on ${remoteLabel}`,
    status: !r ? (terminalFailure.has(state) ? "skipped" : "pending") : r.status === "executed" ? "completed" : r.status === "cancelled" ? "error" : delayOver ? "active" : "pending",
    at: r?.closedAt,
    detail: r?.status === "executed" ? "Performed as the receiver's authority." : r?.status === "cancelled" ? "Stopped by the guardian; it will never run." : r && delayOver ? "Anyone may execute it now." : undefined,
    tx: r?.closedTx ? { hash: r.closedTx, remote: true } : undefined,
  });
  return steps;
}

function iso(seconds: number): string | undefined {
  return seconds > 0 ? new Date(seconds * 1000).toISOString() : undefined;
}
