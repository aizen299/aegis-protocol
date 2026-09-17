import type { ChainName } from "./chains";
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

// Mirrors backend/pkg/types.VaultPosition: indexed totals, raw integers as strings.
export type VaultPosition = {
  chainId: number;
  user: string;
  asset?: string;
  shares: string;
  depositedTotal: string;
  withdrawnTotal: string;
  decimals: number;
  shareDecimals: number;
  lastDepositAt?: string;
};

export type VaultDeposit = {
  chainId: number;
  user: string;
  asset: string;
  vault: string;
  amount: string;
  shares: string;
  decimals: number;
  txHash: string;
  logIndex: number;
  blockNumber: number;
  depositedAt: string;
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
  remote?: RemoteAction;
};

// Mirrors backend/pkg/types.RemoteAction: a governance action received on another chain.
export type RemoteAction = {
  chainId: number;
  receiver: string;
  emitterChain: number;
  sequence: string;
  sourceChainId: number;
  operationId: string;
  target: string;
  declaredValue: string;
  accountsHash: string;
  status: "pending" | "executed" | "cancelled";
  executableAt: string;
  receivedAt: string;
  receivedTx: string;
  closedAt?: string;
  closedTx?: string;
  closedBy?: string;
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

export type ZkGateMetadata = {
  chainId: number;
  address: string;
  tree: string;
  verifier: string;
};

// No owner field, and that is the design. The anonymity this module provides is the difficulty of
// linking a commitment to the action that spends it, and a column joining the two would hand that
// away — so the UI has nothing to join either.
export type Commitment = {
  chainId: number;
  tree: string;
  leafIndex: number;
  commitment: string;
  rootAfter: string;
  txHash: string;
  logIndex: number;
  blockNumber: number;
  insertedAt: string;
};

export type ZkAction = {
  chainId: number;
  gate: string;
  actionId: string;
  name: string;
  registered: boolean;
  blockNumber: number;
};

export type PrivateAction = {
  chainId: number;
  gate: string;
  nullifier: string;
  actionId: string;
  root: string;
  txHash: string;
  logIndex: number;
  blockNumber: number;
  executedAt: string;
};

export type AnonymitySet = {
  chainId: number;
  tree: string;
  leafCount: number;
  currentRoot?: string;
};

export type NullifierStatus = {
  chainId: number;
  gate: string;
  nullifier: string;
  spent: boolean;
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
async function get<T>(chain: ChainName, path: string): Promise<T> {
  const url = new URL(`${env.apiUrl}${path}`);
  url.searchParams.set("chain", chain);
  let res: Response;
  try {
    res = await fetch(url, { cache: "no-store" });
  } catch (cause) {
    throw new Error(`${env.apiUrl} is unreachable`, { cause });
  }

  if (!res.ok) throw new Error(`${path} returned ${res.status}`);

  return (await res.json()) as T;
}

type C = ChainName;

export const api = {
  health: async (): Promise<boolean> => {
    try {
      return (await fetch(`${env.apiUrl}/health`, { cache: "no-store" })).ok;
    } catch {
      return false;
    }
  },

  tvl: (c: C, vault: string) => get<Tvl>(c, `/v1/vault/${vault}/tvl`),
  vaultPosition: (c: C, owner: string) => get<VaultPosition>(c, `/v1/vault/positions/${owner}`),
  vaultDeposits: (c: C, owner: string) => get<Omit<Paged<VaultDeposit>, "count">>(c, `/v1/vault/positions/${owner}/deposits`),

  oracleFeeds: (c: C) => get<Paged<OracleFeed>>(c, "/v1/oracle/feeds"),
  oracleFeed: (c: C, feedId: string) => get<OracleFeed>(c, `/v1/oracle/feeds/${feedId}`),
  oracleRounds: (c: C, feedId: string) => get<Paged<OracleRound>>(c, `/v1/oracle/feeds/${feedId}/rounds`),
  oracleRound: (c: C, roundId: string) => get<OracleRound>(c, `/v1/oracle/rounds/${roundId}`),
  oracleSubmissions: (c: C, roundId: string) =>
    get<Paged<OracleSubmission>>(c, `/v1/oracle/rounds/${roundId}/submissions`),
  oracleNodes: (c: C) => get<Paged<OracleNode>>(c, "/v1/oracle/nodes"),

  governor: (c: C) => get<GovernorMetadata>(c, "/v1/governance/governor"),
  proposals: (c: C) => get<Paged<Proposal>>(c, "/v1/governance/proposals"),
  proposal: (c: C, id: string) => get<Proposal>(c, `/v1/governance/proposals/${id}`),
  proposalVotes: (c: C, id: string) => get<Paged<Vote>>(c, `/v1/governance/proposals/${id}/votes`),
  remoteActions: (c: C, status?: string) =>
    get<Paged<RemoteAction>>(c, `/v1/governance/remote-actions${status ? `?status=${status}` : ""}`),

  zkGate: (c: C) => get<ZkGateMetadata>(c, "/v1/zk/gate"),
  anonymitySet: (c: C) => get<AnonymitySet>(c, "/v1/zk/anonymity-set"),
  commitments: (c: C) => get<Paged<Commitment>>(c, "/v1/zk/commitments"),
  // Paged by limit and offset only. There is no lookup by commitment, deliberately: asking which leaf
  // holds a commitment would tell the server which deposit is about to be spent.
  commitmentsPage: (c: C, limit: number, offset: number) =>
    get<Paged<Commitment>>(c, `/v1/zk/commitments?limit=${limit}&offset=${offset}`),
  zkActions: (c: C) => get<Paged<ZkAction>>(c, "/v1/zk/actions"),
  privateActions: (c: C) => get<Paged<PrivateAction>>(c, "/v1/zk/private-actions"),
  nullifierStatus: (c: C, nullifier: string) =>
    get<NullifierStatus>(c, `/v1/zk/nullifiers/${nullifier}`),
};
