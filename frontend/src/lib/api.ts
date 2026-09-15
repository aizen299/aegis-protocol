import { env } from "./env";

// Indexed history comes from the backend, not the chain: the UI must not re-implement indexing.
// See docs/v1.1-frontend-plan.md §2.3 for which figures come from where.

export type Paged<T> = {
  chainId: number;
  items: T[];
  limit: number;
  offset: number;
  count: number;
};

export type Tvl = {
  chainId: number;
  tvl: { vault: string; asset?: string; amount: string; decimals: number };
};

// Mirrors backend/pkg/types.OracleFeed exactly. A feed carries no latest value — that lives on its
// most recent settled round, which is a separate fetch.
export type OracleFeed = {
  chainId: number;
  feedId: string;
  name: string;
  decimals: number;
  active: boolean;
};

export type OracleRound = {
  chainId: number;
  roundId: string;
  feedId: string;
  feedName: string;
  state: string;
  openedAt: string;
  deadline: string;
  settledAt?: string;
  eligibleCount: number;
  nodeSetVersion: string;
  submissionCount: number;
  aggregatedValue: string;
  decimals: number;
  txHash: string;
  blockNumber: number;
};

export type OracleSubmission = {
  chainId: number;
  roundId: string;
  node: string;
  value: string;
  decimals: number;
  nonce: string;
  nonceKnown: boolean;
  isOutlier: boolean;
  txHash: string;
  logIndex: number;
  blockNumber: number;
  submittedAt: string;
};

export type OracleNode = {
  chainId: number;
  address: string;
  stakeAsset: string;
  stakedAmount: string;
  slashedTotal: string;
  pendingUnstake: string;
  decimals: number;
  claimableAt?: string;
  missedRounds: number;
  active: boolean;
  registeredAt: string;
};

export type ProposalAction = {
  targetChainId: number;
  target: string;
  value: string;
  calldata: string;
};

export type Proposal = {
  chainId: number;
  governor: string;
  proposalId: string;
  proposer: string;
  title: string;
  description?: string;
  action: ProposalAction;
  state: string;
  voteStart: number;
  voteEnd: number;
  votesFor: string;
  votesAgainst: string;
  votesAbstain: string;
  voteDecimals: number;
  operationId?: string;
  executableAt?: string;
  queuedAt?: string;
  dispatchedAt?: string;
  executedAt?: string;
  cancelledAt?: string;
  txHash: string;
  logIndex: number;
  blockNumber: number;
};

export type Vote = {
  chainId: number;
  proposalId: string;
  voter: string;
  support: number;
  weight: string;
  voteDecimals: number;
  reason?: string;
  txHash: string;
  logIndex: number;
  blockNumber: number;
  votedAt: string;
};

export type GovernorMetadata = {
  chainId: number;
  address: string;
  token: string;
  timelock: string;
  tokenDecimals: number;
};

// Throws rather than returning null. A caller handed null cannot tell "the API is unreachable" from
// "there is nothing here", which is the same collapse the vault dashboard was fixed for; throwing
// hands react-query a real error it can report.
async function get<T>(path: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${env.apiUrl}${path}`, { cache: "no-store" });
  } catch (cause) {
    throw new Error(`${env.apiUrl} is unreachable`, { cause });
  }

  if (!res.ok) throw new Error(`${path} returned ${res.status}`);

  return (await res.json()) as T;
}

export const api = {
  tvl: (vault: string) => get<Tvl>(`/v1/vault/${vault}/tvl`),

  oracleFeeds: () => get<Paged<OracleFeed>>("/v1/oracle/feeds"),
  oracleFeed: (feedId: string) => get<OracleFeed>(`/v1/oracle/feeds/${feedId}`),
  oracleRounds: (feedId: string) => get<Paged<OracleRound>>(`/v1/oracle/feeds/${feedId}/rounds`),
  oracleRound: (roundId: string) => get<OracleRound>(`/v1/oracle/rounds/${roundId}`),
  oracleSubmissions: (roundId: string) =>
    get<Paged<OracleSubmission>>(`/v1/oracle/rounds/${roundId}/submissions`),
  oracleNodes: () => get<Paged<OracleNode>>("/v1/oracle/nodes"),

  governor: () => get<GovernorMetadata>("/v1/governance/governor"),
  proposals: () => get<Paged<Proposal>>("/v1/governance/proposals"),
  proposal: (id: string) => get<Proposal>(`/v1/governance/proposals/${id}`),
  proposalVotes: (id: string) => get<Paged<Vote>>(`/v1/governance/proposals/${id}/votes`),
};
