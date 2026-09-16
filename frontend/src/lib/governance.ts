// Whether an account can act on a proposal, and if not, the real reason.
//
// The reasons are the product here. castVote reverts with NoVotingPower in several situations that
// look identical from outside and have different remedies — a holder who never delegated, one who
// delegated after the snapshot, one whose tokens vote through someone else. A disabled button that
// says why is the difference between a user who understands governance and one who thinks it is
// broken. See docs/v1.3-write-actions-plan.md §2.4.

// Mirrors IGovernor.ProposalState. Read from the chain, not the API: the indexer lags by the
// confirmation depth, and "can I act now" is a present-tense question. §2.3.
export const ProposalState = {
  NONE: 0,
  PENDING: 1,
  ACTIVE: 2,
  SUCCEEDED: 3,
  DEFEATED: 4,
  QUEUED: 5,
  DISPATCHED: 6,
  EXECUTED: 7,
  FAILED: 8,
  CANCELLED: 9,
} as const;

const stateNames = [
  "unknown",
  "pending",
  "active",
  "succeeded",
  "defeated",
  "queued",
  "dispatched",
  "executed",
  "failed",
  "cancelled",
];

export function stateName(state: number): string {
  return stateNames[state] ?? `unknown (${state})`;
}

export const ZERO_ADDRESS = "0x0000000000000000000000000000000000000000";

export type Eligibility = { allowed: true; weight: bigint } | { allowed: false; reason: string };

export function voteEligibility(input: {
  connected: boolean;
  account?: string;
  state?: number;
  voteStart: number;
  hasVoted?: boolean;
  // Undefined until voting opens: getPastVotes reverts for a timepoint that is not yet past.
  pastVotes?: bigint;
  currentVotes?: bigint;
  balance?: bigint;
  delegatee?: string;
}): Eligibility {
  if (!input.connected || !input.account) {
    return { allowed: false, reason: "Connect a wallet to vote." };
  }
  if (input.state === undefined) {
    return { allowed: false, reason: "Reading this proposal's current state…" };
  }
  if (input.state === ProposalState.PENDING) {
    return {
      allowed: false,
      reason: `Voting has not opened. It opens ${new Date(input.voteStart * 1000).toLocaleString()}.`,
    };
  }
  if (input.state !== ProposalState.ACTIVE) {
    return { allowed: false, reason: `Voting is closed — this proposal is ${stateName(input.state)}.` };
  }
  if (input.hasVoted) {
    return { allowed: false, reason: "You have already voted on this proposal." };
  }
  if (input.pastVotes === undefined) {
    return { allowed: false, reason: "Reading your voting power at this proposal's snapshot…" };
  }
  if (input.pastVotes > 0n) {
    return { allowed: true, weight: input.pastVotes };
  }

  // No weight at the snapshot. Which of several causes it is decides what the holder should do.
  const account = input.account.toLowerCase();
  const delegatee = input.delegatee?.toLowerCase();

  if (input.currentVotes !== undefined && input.currentVotes > 0n) {
    return {
      allowed: false,
      reason:
        "You have voting power now, but you had none when this proposal's snapshot was taken, so " +
        "it does not count here. It will count for proposals created from now on.",
    };
  }
  if (delegatee && delegatee !== ZERO_ADDRESS && delegatee !== account) {
    return {
      allowed: false,
      reason: `Your tokens are delegated to ${input.delegatee}, so they vote there rather than here.`,
    };
  }
  if (input.balance !== undefined && input.balance > 0n && (!delegatee || delegatee === ZERO_ADDRESS)) {
    return {
      allowed: false,
      reason:
        "You hold governance tokens but have never delegated them, and undelegated tokens carry no " +
        "voting power. Delegating to yourself now cannot apply to this proposal — its snapshot has " +
        "passed — but it will for the next one.",
    };
  }
  if (input.balance === 0n) {
    return { allowed: false, reason: "You hold no governance tokens." };
  }
  return { allowed: false, reason: "You had no voting power when this proposal's snapshot was taken." };
}

export type Availability = { allowed: true } | { allowed: false; reason: string };

export function queueAvailability(input: { connected: boolean; state?: number }): Availability {
  if (!input.connected) return { allowed: false, reason: "Connect a wallet to queue." };
  if (input.state === undefined) return { allowed: false, reason: "Reading this proposal's current state…" };
  if (input.state !== ProposalState.SUCCEEDED) {
    return {
      allowed: false,
      reason: `Only a succeeded proposal can be queued — this one is ${stateName(input.state)}.`,
    };
  }
  return { allowed: true };
}

export function executeAvailability(input: {
  connected: boolean;
  state?: number;
  // Both in seconds of chain time, both read from the chain. executableAt comes from the Governor's
  // own proposal record, not the API: the indexer lags, and "can I act now" is a present-tense
  // question. §2.3.
  executableAt?: number;
  now?: number;
}): Availability {
  if (!input.connected) return { allowed: false, reason: "Connect a wallet to execute." };
  if (input.state === undefined) return { allowed: false, reason: "Reading this proposal's current state…" };
  if (input.state !== ProposalState.QUEUED) {
    return {
      allowed: false,
      reason: `Only a queued proposal can be executed — this one is ${stateName(input.state)}.`,
    };
  }

  // An unknown time refuses; it never permits. This was previously the reverse: an executable time
  // not yet indexed skipped the check entirely, and a missing block time fell back to the browser's
  // clock. Against a local chain eight days ahead of the wall clock, Execute was offered two days
  // before the timelock would accept it.
  if (input.executableAt === undefined || input.now === undefined) {
    return { allowed: false, reason: "Reading the timelock…" };
  }
  if (!Number.isFinite(input.executableAt) || !Number.isFinite(input.now) || input.executableAt <= 0) {
    return { allowed: false, reason: "The timelock's executable time could not be read." };
  }
  if (input.now < input.executableAt) {
    return {
      allowed: false,
      reason: `The timelock delay has not elapsed. Executable ${new Date(input.executableAt * 1000).toLocaleString()}.`,
    };
  }
  return { allowed: true };
}
