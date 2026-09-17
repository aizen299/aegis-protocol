"use client";

import { useQuery } from "@tanstack/react-query";

import { api } from "./api";
import { useChain } from "./chainContext";

// Indexed data moves on the indexer's poll, not the browser's. Ten seconds is under the poll
// interval, so a refetch costs little and a stale figure is short-lived.
const staleTime = 10_000;

// react-query retries three times with exponential backoff by default, and a query stays pending
// throughout. A persistently unreachable API therefore renders as "loading…" for tens of seconds —
// the same collapse of failure into absence this project fixed for contract reads, reappearing one
// layer up. One retry covers a dropped connection; anything past that is a failure worth showing.
const retry = 1;
const retryDelay = 500;

const shared = { staleTime, retry, retryDelay };

export function useOracleFeeds() {
  const chain = useChain().name;
  return useQuery({ queryKey: [chain, "oracle", "feeds"], queryFn: () => api.oracleFeeds(chain), ...shared });
}

export function useOracleFeed(feedId: string) {
  const chain = useChain().name;
  return useQuery({
    queryKey: [chain, "oracle", "feed", feedId],
    queryFn: () => api.oracleFeed(chain, feedId),
    ...shared,
    enabled: Boolean(feedId),
  });
}

export function useOracleRounds(feedId: string) {
  const chain = useChain().name;
  return useQuery({
    queryKey: [chain, "oracle", "rounds", feedId],
    queryFn: () => api.oracleRounds(chain, feedId),
    ...shared,
    enabled: Boolean(feedId),
  });
}

export function useOracleRound(roundId: string) {
  const chain = useChain().name;
  return useQuery({
    queryKey: [chain, "oracle", "round", roundId],
    queryFn: () => api.oracleRound(chain, roundId),
    ...shared,
    enabled: Boolean(roundId),
  });
}

export function useOracleSubmissions(roundId: string) {
  const chain = useChain().name;
  return useQuery({
    queryKey: [chain, "oracle", "submissions", roundId],
    queryFn: () => api.oracleSubmissions(chain, roundId),
    ...shared,
    enabled: Boolean(roundId),
  });
}

export function useOracleNodes() {
  const chain = useChain().name;
  return useQuery({ queryKey: [chain, "oracle", "nodes"], queryFn: () => api.oracleNodes(chain), ...shared });
}

export function useProposals() {
  const chain = useChain().name;
  return useQuery({ queryKey: [chain, "governance", "proposals"], queryFn: () => api.proposals(chain), ...shared });
}

export function useProposal(id: string) {
  const chain = useChain().name;
  return useQuery({
    queryKey: [chain, "governance", "proposal", id],
    queryFn: () => api.proposal(chain, id),
    ...shared,
    enabled: Boolean(id),
  });
}

export function useProposalVotes(id: string) {
  const chain = useChain().name;
  return useQuery({
    queryKey: [chain, "governance", "votes", id],
    queryFn: () => api.proposalVotes(chain, id),
    ...shared,
    enabled: Boolean(id),
  });
}

export function useZkGate() {
  const chain = useChain().name;
  return useQuery({ queryKey: [chain, "zk", "gate"], queryFn: () => api.zkGate(chain), ...shared });
}

export function useAnonymitySet() {
  const chain = useChain().name;
  return useQuery({ queryKey: [chain, "zk", "anonymity"], queryFn: () => api.anonymitySet(chain), ...shared });
}

export function useCommitments() {
  const chain = useChain().name;
  return useQuery({ queryKey: [chain, "zk", "commitments"], queryFn: () => api.commitments(chain), ...shared });
}

export function useZkActions() {
  const chain = useChain().name;
  return useQuery({ queryKey: [chain, "zk", "actions"], queryFn: () => api.zkActions(chain), ...shared });
}

export function usePrivateActions() {
  const chain = useChain().name;
  return useQuery({ queryKey: [chain, "zk", "private-actions"], queryFn: () => api.privateActions(chain), ...shared });
}

export function useNullifierStatus(nullifier: string) {
  const chain = useChain().name;
  return useQuery({
    queryKey: [chain, "zk", "nullifier", nullifier],
    queryFn: () => api.nullifierStatus(chain, nullifier),
    ...shared,
    enabled: /^0x[0-9a-f]{64}$/.test(nullifier),
    retry: false,
  });
}

export function useGovernor() {
  const chain = useChain().name;
  return useQuery({ queryKey: [chain, "governance", "governor"], queryFn: () => api.governor(chain), ...shared });
}

export function useRemoteActions(status?: string) {
  const chain = useChain().name;
  return useQuery({
    queryKey: [chain, "governance", "remote-actions", status ?? ""],
    queryFn: () => api.remoteActions(chain, status),
    ...shared,
  });
}

export function useApiHealth() {
  return useQuery({ queryKey: ["health"], queryFn: api.health, refetchInterval: 15_000, retry: false });
}
