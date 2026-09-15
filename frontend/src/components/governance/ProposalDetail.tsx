"use client";

import { Async } from "@/components/Async";
import { Panel, Stat } from "@/components/Panel";
import { StateBadge } from "@/components/StateBadge";
import { Cell, Row, Table } from "@/components/Table";
import { formatTime, formatUnixTime, relativeToNow, shortAddress } from "@/lib/format";
import { useProposal, useProposalVotes } from "@/lib/queries";
import { failed, value as valueOf } from "@/lib/readState";
import { formatRaw } from "@/lib/units";

const supportLabels = ["against", "for", "abstain"];

export function ProposalDetail({ proposalId }: { proposalId: string }) {
  const proposal = useProposal(proposalId);

  return (
    <div className="flex flex-col gap-5">
      <Async query={proposal} empty="This proposal does not exist.">
        {(data) => {
          const tally = (raw: string) => {
            const text = formatRaw(raw, data.voteDecimals);
            return text === undefined ? failed("the weight could not be read") : valueOf(text);
          };

          return (
            <>
              <Panel title={data.title || `Proposal ${proposalId}`}>
                <div className="mb-3">
                  <StateBadge state={data.state} />
                </div>
                {data.description ? (
                  <p className="mb-4 whitespace-pre-wrap text-sm text-zinc-400">
                    {data.description}
                  </p>
                ) : null}
                <Stat label="Proposer" state={valueOf(shortAddress(data.proposer))} />
                <Stat label="Voting opens" state={valueOf(formatUnixTime(data.voteStart))} />
                <Stat label="Voting closes" state={valueOf(formatUnixTime(data.voteEnd))} />
              </Panel>

              <Panel title="Tally">
                <Stat label="For" state={tally(data.votesFor)} />
                <Stat label="Against" state={tally(data.votesAgainst)} />
                <Stat label="Abstain" state={tally(data.votesAbstain)} />
              </Panel>

              <Panel title="Timelock">
                {data.executableAt ? (
                  <>
                    <Stat
                      label="Executable at"
                      state={valueOf(
                        `${formatTime(data.executableAt)} (${relativeToNow(data.executableAt)})`,
                      )}
                    />
                    <Stat
                      label="Operation"
                      state={data.operationId ? valueOf(data.operationId) : failed("not queued")}
                    />
                  </>
                ) : (
                  <p className="text-sm text-zinc-500">
                    Not queued. A proposal reaches the timelock only after it succeeds.
                  </p>
                )}
                <Stat label="Executed" state={valueOf(formatTime(data.executedAt))} />
                <Stat label="Cancelled" state={valueOf(formatTime(data.cancelledAt))} />
              </Panel>

              <Panel title="Action">
                {/* Shown in full, including calldata. A proposal a voter cannot read is one they
                    are trusting rather than judging. */}
                <Stat label="Target chain" state={valueOf(String(data.action.targetChainId))} />
                <Stat label="Target" state={valueOf(data.action.target)} />
                <Stat label="Value" state={valueOf(data.action.value)} />
                <p className="mt-3 break-all font-mono text-xs text-zinc-400">
                  {data.action.calldata}
                </p>
              </Panel>
            </>
          );
        }}
      </Async>

      <VoteList proposalId={proposalId} />
    </div>
  );
}

function VoteList({ proposalId }: { proposalId: string }) {
  const votes = useProposalVotes(proposalId);

  return (
    <Panel title="Votes">
      <Async query={{ ...votes, data: votes.data?.items }} empty="Nobody has voted yet.">
        {(items) => (
          <Table headers={["Voter", "Support", "Weight", "Reason", "Cast"]}>
            {items.map((vote) => (
              <Row key={`${vote.txHash}:${vote.logIndex}`}>
                <Cell mono>{shortAddress(vote.voter)}</Cell>
                <Cell>{supportLabels[vote.support] ?? `unknown (${vote.support})`}</Cell>
                <Cell mono>
                  {formatRaw(vote.weight, vote.voteDecimals) ?? "unavailable"}
                </Cell>
                <Cell>{vote.reason || "—"}</Cell>
                <Cell>{formatTime(vote.votedAt)}</Cell>
              </Row>
            ))}
          </Table>
        )}
      </Async>
    </Panel>
  );
}
