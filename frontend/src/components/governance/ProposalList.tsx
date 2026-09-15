"use client";

import Link from "next/link";

import { Async } from "@/components/Async";
import { Panel } from "@/components/Panel";
import { StateBadge } from "@/components/StateBadge";
import { Cell, Row, Table } from "@/components/Table";
import { formatUnixTime, shortAddress } from "@/lib/format";
import { useProposals } from "@/lib/queries";
import { formatRaw } from "@/lib/units";

export function ProposalList() {
  const proposals = useProposals();

  return (
    <Panel title="Proposals">
      <Async
        query={{ ...proposals, data: proposals.data?.items }}
        empty="No proposals have been created."
      >
        {(items) => (
          <Table headers={["Proposal", "State", "For", "Against", "Proposer", "Voting ends"]}>
            {items.map((proposal) => (
              <Row key={proposal.proposalId}>
                <Cell>
                  <Link
                    className="text-sky-300 hover:underline"
                    href={`/governance/${proposal.proposalId}`}
                  >
                    {proposal.title || proposal.proposalId}
                  </Link>
                </Cell>
                <Cell>
                  <StateBadge state={proposal.state} />
                </Cell>
                <Cell mono>
                  {formatRaw(proposal.votesFor, proposal.voteDecimals) ?? "unavailable"}
                </Cell>
                <Cell mono>
                  {formatRaw(proposal.votesAgainst, proposal.voteDecimals) ?? "unavailable"}
                </Cell>
                <Cell mono>{shortAddress(proposal.proposer)}</Cell>
                <Cell>{formatUnixTime(proposal.voteEnd)}</Cell>
              </Row>
            ))}
          </Table>
        )}
      </Async>
    </Panel>
  );
}
