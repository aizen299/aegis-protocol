"use client";

import { useAccount, useBlock, useReadContract } from "wagmi";

import { Panel } from "@/components/Panel";
import { TxButton, TxStatus } from "@/components/TxButton";
import { governorAbi, votesTokenAbi } from "@/lib/abi";
import {
  ProposalState,
  ZERO_ADDRESS,
  executeAvailability,
  queueAvailability,
  voteEligibility,
  type Availability,
  type Eligibility,
} from "@/lib/governance";
import { useGovernor } from "@/lib/queries";
import { useTx } from "@/lib/tx";
import { formatAmount } from "@/lib/units";

type Address = `0x${string}`;

export function ProposalActions({
  proposalId,
  voteStart,
  executableAt,
}: {
  proposalId: string;
  voteStart: number;
  executableAt?: string;
}) {
  const governor = useGovernor();
  const governorAddress = governor.data?.address as Address | undefined;
  const tokenAddress = governor.data?.token as Address | undefined;
  const decimals = governor.data?.tokenDecimals;

  const { address: account, isConnected } = useAccount();
  const id = BigInt(proposalId);

  const { data: state } = useReadContract({
    address: governorAddress,
    abi: governorAbi,
    functionName: "state",
    args: [id],
    query: { enabled: Boolean(governorAddress), refetchInterval: 5_000 },
  });

  const { data: hasVoted } = useReadContract({
    address: governorAddress,
    abi: governorAbi,
    functionName: "hasVoted",
    args: account ? [id, account] : undefined,
    query: { enabled: Boolean(governorAddress && account) },
  });

  // getPastVotes reverts for a timepoint that is not yet past, so it is not asked until voting has
  // opened. Asking earlier produces an error that would otherwise read as "no voting power".
  const votingOpened = state !== undefined && state !== ProposalState.PENDING;
  const { data: pastVotes } = useReadContract({
    address: tokenAddress,
    abi: votesTokenAbi,
    functionName: "getPastVotes",
    args: account ? [account, BigInt(voteStart)] : undefined,
    query: { enabled: Boolean(tokenAddress && account && votingOpened) },
  });

  const { data: currentVotes } = useReadContract({
    address: tokenAddress,
    abi: votesTokenAbi,
    functionName: "getVotes",
    args: account ? [account] : undefined,
    query: { enabled: Boolean(tokenAddress && account) },
  });

  const { data: balance } = useReadContract({
    address: tokenAddress,
    abi: votesTokenAbi,
    functionName: "balanceOf",
    args: account ? [account] : undefined,
    query: { enabled: Boolean(tokenAddress && account) },
  });

  const { data: delegatee } = useReadContract({
    address: tokenAddress,
    abi: votesTokenAbi,
    functionName: "delegates",
    args: account ? [account] : undefined,
    query: { enabled: Boolean(tokenAddress && account) },
  });

  // Chain time, not the browser's clock — the timelock compares against block timestamps.
  const { data: block } = useBlock({ query: { refetchInterval: 5_000 } });
  const chainNowMs = block ? Number(block.timestamp) * 1000 : undefined;

  const vote = voteEligibility({
    connected: isConnected,
    account,
    state,
    voteStart,
    hasVoted,
    pastVotes,
    currentVotes,
    balance,
    delegatee,
  });
  const queue = queueAvailability({ connected: isConnected, state });
  const execute = executeAvailability({
    connected: isConnected,
    state,
    executableAt,
    now: chainNowMs,
  });

  const tx = useTx();
  const neverDelegated =
    balance !== undefined && balance > 0n && (!delegatee || delegatee === ZERO_ADDRESS);

  function castVote(support: 0 | 1 | 2) {
    if (!governorAddress || !vote.allowed) return;
    tx.writeContract({
      address: governorAddress,
      abi: governorAbi,
      functionName: "castVote",
      args: [id, support, ""],
    });
  }

  function send(functionName: "queue" | "execute") {
    if (!governorAddress) return;
    tx.writeContract({ address: governorAddress, abi: governorAbi, functionName, args: [id] });
  }

  function delegateToSelf() {
    if (!tokenAddress || !account) return;
    tx.writeContract({
      address: tokenAddress,
      abi: votesTokenAbi,
      functionName: "delegate",
      args: [account],
    });
  }

  return (
    <Panel title="Act on this proposal">
      <section className="mb-5">
        <h3 className="mb-2 text-xs uppercase tracking-wide text-zinc-500">Vote</h3>
        <VotingPower eligibility={vote} decimals={decimals} />
        <div className="mt-3 grid grid-cols-3 gap-2">
          <TxButton disabled={!vote.allowed} pending={tx.busy} onClick={() => castVote(1)}>
            For
          </TxButton>
          <TxButton disabled={!vote.allowed} pending={tx.busy} onClick={() => castVote(0)}>
            Against
          </TxButton>
          <TxButton disabled={!vote.allowed} pending={tx.busy} onClick={() => castVote(2)}>
            Abstain
          </TxButton>
        </div>
        {neverDelegated ? (
          <div className="mt-3">
            <TxButton pending={tx.busy} onClick={delegateToSelf}>
              Delegate to myself — for future proposals
            </TxButton>
          </div>
        ) : null}
      </section>

      <section className="mb-5">
        <h3 className="mb-2 text-xs uppercase tracking-wide text-zinc-500">Queue</h3>
        <Reason availability={queue} />
        <TxButton disabled={!queue.allowed} pending={tx.busy} onClick={() => send("queue")}>
          Queue in the timelock
        </TxButton>
      </section>

      <section>
        <h3 className="mb-2 text-xs uppercase tracking-wide text-zinc-500">Execute</h3>
        <Reason availability={execute} />
        <TxButton disabled={!execute.allowed} pending={tx.busy} onClick={() => send("execute")}>
          Execute
        </TxButton>
      </section>

      <TxStatus phase={tx.phase} />
    </Panel>
  );
}

function VotingPower({
  eligibility,
  decimals,
}: {
  eligibility: Eligibility;
  decimals: number | undefined;
}) {
  if (eligibility.allowed) {
    return (
      <p className="text-sm text-zinc-300" data-testid="vote-allowed">
        You can vote with{" "}
        <span className="font-mono">{formatAmount(eligibility.weight, decimals) ?? "…"}</span> votes —
        your power at this proposal&apos;s snapshot.
      </p>
    );
  }
  return (
    <p className="text-sm text-zinc-400" role="status" data-testid="vote-reason">
      {eligibility.reason}
    </p>
  );
}

function Reason({ availability }: { availability: Availability }) {
  if (availability.allowed) return null;
  return (
    <p className="mb-2 text-sm text-zinc-500" role="status">
      {availability.reason}
    </p>
  );
}
