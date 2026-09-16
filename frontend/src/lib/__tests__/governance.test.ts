import { describe, expect, it } from "vitest";

import {
  ProposalState,
  ZERO_ADDRESS,
  executeAvailability,
  queueAvailability,
  voteEligibility,
} from "../governance";

const account = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";
const active = {
  connected: true,
  account,
  state: ProposalState.ACTIVE,
  voteStart: 1_789_000_000,
  hasVoted: false,
};

const reasonOf = (e: ReturnType<typeof voteEligibility>) => (e.allowed ? "" : e.reason);

describe("voteEligibility", () => {
  it("allows a holder with weight at the snapshot, carrying that weight", () => {
    expect(voteEligibility({ ...active, pastVotes: 5n })).toEqual({ allowed: true, weight: 5n });
  });

  // The v1.1 plan's §2.2 trap, stated in the words a holder needs.
  it("tells a holder who never delegated that undelegated tokens carry no power", () => {
    const e = voteEligibility({
      ...active,
      pastVotes: 0n,
      currentVotes: 0n,
      balance: 1000n,
      delegatee: ZERO_ADDRESS,
    });
    expect(e.allowed).toBe(false);
    expect(reasonOf(e)).toMatch(/never delegated/i);
    // And that fixing it now does not rescue this proposal.
    expect(reasonOf(e)).toMatch(/cannot apply to this proposal/i);
  });

  it("distinguishes delegating after the snapshot from never delegating", () => {
    const e = voteEligibility({ ...active, pastVotes: 0n, currentVotes: 1000n, delegatee: account });
    expect(reasonOf(e)).toMatch(/snapshot/i);
    expect(reasonOf(e)).not.toMatch(/never delegated/i);
  });

  it("says where the votes went when they are delegated to someone else", () => {
    const other = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8";
    const e = voteEligibility({
      ...active,
      pastVotes: 0n,
      currentVotes: 0n,
      balance: 1000n,
      delegatee: other,
    });
    expect(reasonOf(e)).toContain(other);
  });

  it("recognises self-delegation regardless of address casing", () => {
    const e = voteEligibility({
      ...active,
      pastVotes: 0n,
      currentVotes: 0n,
      balance: 0n,
      delegatee: account.toLowerCase(),
    });
    expect(reasonOf(e)).not.toMatch(/delegated to/i);
  });

  it("refuses a second vote", () => {
    expect(reasonOf(voteEligibility({ ...active, hasVoted: true, pastVotes: 5n }))).toMatch(
      /already voted/i,
    );
  });

  it("says voting has not opened for a pending proposal", () => {
    const e = voteEligibility({ ...active, state: ProposalState.PENDING });
    expect(reasonOf(e)).toMatch(/not opened/i);
  });

  it("names the state of a closed proposal", () => {
    expect(reasonOf(voteEligibility({ ...active, state: ProposalState.DEFEATED }))).toMatch(/defeated/);
  });

  // The snapshot read reverts before voting opens, and a missing read must not become "no power".
  it("does not report no power while the snapshot read is still pending", () => {
    const e = voteEligibility({ ...active, pastVotes: undefined });
    expect(e.allowed).toBe(false);
    expect(reasonOf(e)).not.toMatch(/no voting power|never delegated|hold no/i);
  });

  it("does not allow voting until the live state is known", () => {
    expect(voteEligibility({ ...active, state: undefined, pastVotes: 5n }).allowed).toBe(false);
  });

  it("asks for a wallet first", () => {
    expect(reasonOf(voteEligibility({ ...active, connected: false }))).toMatch(/connect a wallet/i);
  });
});

describe("queueAvailability", () => {
  it("allows queueing only a succeeded proposal", () => {
    expect(queueAvailability({ connected: true, state: ProposalState.SUCCEEDED }).allowed).toBe(true);
    for (const state of [ProposalState.ACTIVE, ProposalState.DEFEATED, ProposalState.QUEUED]) {
      expect(queueAvailability({ connected: true, state }).allowed).toBe(false);
    }
  });
});

describe("executeAvailability", () => {
  const executableAt = "2026-09-20T00:00:00Z";
  const at = Date.parse(executableAt);

  it("refuses before the timelock delay elapses", () => {
    const e = executeAvailability({
      connected: true,
      state: ProposalState.QUEUED,
      executableAt,
      now: at - 1,
    });
    expect(e.allowed).toBe(false);
  });

  it("allows execution at the executable time", () => {
    expect(
      executeAvailability({ connected: true, state: ProposalState.QUEUED, executableAt, now: at })
        .allowed,
    ).toBe(true);
  });

  it("refuses anything that is not queued", () => {
    expect(
      executeAvailability({ connected: true, state: ProposalState.SUCCEEDED, executableAt, now: at })
        .allowed,
    ).toBe(false);
  });
});
