import { describe, expect, it } from "vitest";

import type { Proposal, RemoteAction } from "../api";
import { governanceTimeline, isRemote } from "../governanceTimeline";

const now = Date.parse("2026-09-17T12:00:00Z");
const sec = (iso: string) => Date.parse(iso) / 1000;

const base: Proposal = {
  chainId: 31337,
  governor: "0xg",
  proposalId: "1",
  proposer: "0xp",
  title: "t",
  action: { targetChainId: 31337, target: "0x", value: "0", calldata: "0x" },
  state: "active",
  voteStart: sec("2026-09-16T00:00:00Z"),
  voteEnd: sec("2026-09-18T00:00:00Z"),
  votesFor: "0",
  votesAgainst: "0",
  votesAbstain: "0",
  voteDecimals: 18,
  txHash: "0xh",
  logIndex: 0,
  blockNumber: 1,
};

const remoteBase = (over: Partial<Proposal> = {}): Proposal => ({
  ...base,
  action: { ...base.action, targetChainId: 4611686018427387907 },
  state: "dispatched",
  voteEnd: sec("2026-09-16T12:00:00Z"),
  queuedAt: "2026-09-16T13:00:00Z",
  dispatchedAt: "2026-09-17T11:00:00Z",
  operationId: "4",
  ...over,
});

const receipt = (over: Partial<RemoteAction>): RemoteAction => ({
  chainId: 4611686018427387904,
  receiver: "R",
  emitterChain: 23,
  sequence: "0",
  sourceChainId: 31337,
  operationId: "4",
  target: "V",
  declaredValue: "0",
  accountsHash: "0x",
  status: "pending",
  executableAt: "2026-09-17T13:00:00Z",
  receivedAt: "2026-09-17T11:30:00Z",
  receivedTx: "sigR",
  ...over,
});

const status = (steps: ReturnType<typeof governanceTimeline>) => Object.fromEntries(steps.map((s) => [s.id, s.status]));

describe("governance timeline", () => {
  it("shows a local proposal voting, then queued, then executed", () => {
    expect(status(governanceTimeline(base, now, "Solana"))).toEqual({ proposed: "completed", voting: "active", queued: "pending", executed: "pending" });
    const executed = { ...base, state: "executed", voteEnd: sec("2026-09-16T12:00:00Z"), queuedAt: "2026-09-16T13:00:00Z", executedAt: "2026-09-17T00:00:00Z" };
    expect(status(governanceTimeline(executed, now, "Solana"))).toEqual({ proposed: "completed", voting: "completed", queued: "completed", executed: "completed" });
    expect(isRemote(executed)).toBe(false);
  });

  it("marks a defeated proposal's later steps as not applying", () => {
    const defeated = { ...base, state: "defeated", voteEnd: sec("2026-09-16T12:00:00Z") };
    expect(status(governanceTimeline(defeated, now, "Solana"))).toEqual({ proposed: "completed", voting: "error", queued: "skipped", executed: "skipped" });
  });

  // Dispatched is not executed: without a receipt the Solana steps stay open.
  it("waits for relay after dispatch", () => {
    const s = status(governanceTimeline(remoteBase(), now, "Solana"));
    expect(s).toMatchObject({ dispatched: "completed", received: "active", delay: "pending", "remote-outcome": "pending" });
  });

  it("counts down the delay once received, then offers execution", () => {
    const waiting = governanceTimeline(remoteBase({ remote: receipt({}) }), now, "Solana");
    expect(status(waiting)).toMatchObject({ received: "completed", delay: "active", "remote-outcome": "pending" });
    expect(waiting.find((s) => s.id === "received")!.tx).toEqual({ hash: "sigR", remote: true });

    const due = governanceTimeline(remoteBase({ remote: receipt({ executableAt: "2026-09-17T11:59:00Z" }) }), now, "Solana");
    expect(status(due)).toMatchObject({ delay: "completed", "remote-outcome": "active" });
  });

  it("ends executed or cancelled on the destination", () => {
    const executed = governanceTimeline(remoteBase({ remote: receipt({ status: "executed", closedAt: "2026-09-17T11:58:00Z", closedTx: "sigX", executableAt: "2026-09-17T11:50:00Z" }) }), now, "Solana");
    expect(status(executed)).toMatchObject({ delay: "completed", "remote-outcome": "completed" });
    expect(executed.at(-1)!.title).toBe("Executed on Solana");

    const cancelled = governanceTimeline(remoteBase({ remote: receipt({ status: "cancelled", closedAt: "2026-09-17T11:40:00Z", closedTx: "sigC" }) }), now, "Solana");
    expect(status(cancelled)).toMatchObject({ delay: "error", "remote-outcome": "error" });
    expect(cancelled.at(-1)!.title).toBe("Cancelled on Solana");
  });
});
