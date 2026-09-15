"use client";

import { useQuery } from "@tanstack/react-query";

import { api } from "./api";

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
  return useQuery({ queryKey: ["oracle", "feeds"], queryFn: api.oracleFeeds, ...shared });
}

export function useOracleFeed(feedId: string) {
  return useQuery({
    queryKey: ["oracle", "feed", feedId],
    queryFn: () => api.oracleFeed(feedId),
    ...shared,
    enabled: Boolean(feedId),
  });
}

export function useOracleRounds(feedId: string) {
  return useQuery({
    queryKey: ["oracle", "rounds", feedId],
    queryFn: () => api.oracleRounds(feedId),
    ...shared,
    enabled: Boolean(feedId),
  });
}

export function useOracleRound(roundId: string) {
  return useQuery({
    queryKey: ["oracle", "round", roundId],
    queryFn: () => api.oracleRound(roundId),
    ...shared,
    enabled: Boolean(roundId),
  });
}

export function useOracleSubmissions(roundId: string) {
  return useQuery({
    queryKey: ["oracle", "submissions", roundId],
    queryFn: () => api.oracleSubmissions(roundId),
    ...shared,
    enabled: Boolean(roundId),
  });
}

export function useOracleNodes() {
  return useQuery({ queryKey: ["oracle", "nodes"], queryFn: api.oracleNodes, ...shared });
}

export function useProposals() {
  return useQuery({ queryKey: ["governance", "proposals"], queryFn: api.proposals, ...shared });
}

export function useProposal(id: string) {
  return useQuery({
    queryKey: ["governance", "proposal", id],
    queryFn: () => api.proposal(id),
    ...shared,
    enabled: Boolean(id),
  });
}

export function useProposalVotes(id: string) {
  return useQuery({
    queryKey: ["governance", "votes", id],
    queryFn: () => api.proposalVotes(id),
    ...shared,
    enabled: Boolean(id),
  });
}

export function useGovernor() {
  return useQuery({ queryKey: ["governance", "governor"], queryFn: api.governor, ...shared });
}
