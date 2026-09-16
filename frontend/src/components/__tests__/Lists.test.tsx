import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  useOracleNodes: vi.fn(),
  useProposals: vi.fn(),
}));

vi.mock("@/lib/queries", () => ({
  useOracleNodes: mocks.useOracleNodes,
  useProposals: mocks.useProposals,
}));

import { NodeList } from "../oracle/NodeList";
import { ProposalList } from "../governance/ProposalList";

const ok = (items: unknown[]) => ({
  data: { chainId: 31337, items, limit: 100, offset: 0, count: items.length },
  isPending: false,
  isError: false,
  error: null,
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("NodeList", () => {
  const node = {
    chainId: 31337,
    address: "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
    stakeAsset: "0x5FbDB2315678afecb367f032d93F642f64180aa3",
    stakedAmount: "5000000000",
    slashedTotal: "50000000",
    pendingUnstake: "0",
    decimals: 6,
    missedRounds: 12,
    active: true,
    registeredAt: "2026-09-01T00:00:00Z",
  };

  it("scales stake by the stake asset's decimals, not by eighteen", () => {
    mocks.useOracleNodes.mockReturnValue(ok([node]));
    render(<NodeList />);

    expect(screen.getByText("5,000")).toBeInTheDocument();
    expect(screen.getByText("50")).toBeInTheDocument();
  });

  // The page states the penalty rules an operator is held to, and never again the claim it shipped
  // with in v1.1 — that misses were recorded but not penalised, when in fact neither was true.
  it("states the missed-round penalties and what is excused", () => {
    mocks.useOracleNodes.mockReturnValue(ok([node]));
    render(<NodeList />);

    expect(screen.getByText("12")).toBeInTheDocument();

    const rules = screen.getByTestId("missed-round-rules");
    expect(rules).toHaveTextContent(/0\.5%/);
    expect(rules).toHaveTextContent(/third consecutive miss costs 10%/i);
    expect(rules).toHaveTextContent(/settled before its deadline/i);
    expect(rules).toHaveTextContent(/unstake is not an excuse/i);
    expect(rules).not.toHaveTextContent(/not penalised/i);
  });
});

describe("ProposalList", () => {
  const proposal = {
    chainId: 31337,
    governor: "0xa51c1FC2f0D1a1b8494Ed1FE312d7C3a78Ed91C0",
    proposalId: "1",
    proposer: "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
    title: "Raise the deposit cap",
    action: { targetChainId: 31337, target: "0xCf7E", value: "0", calldata: "0x86651203" },
    state: "ACTIVE",
    voteStart: 1_789_000_000,
    voteEnd: 1_789_600_000,
    votesFor: "1000000000000000000000",
    votesAgainst: "0",
    votesAbstain: "0",
    voteDecimals: 18,
    txHash: "0xabc",
    blockNumber: 4,
  };

  it("scales vote weight by the token's decimals", () => {
    mocks.useProposals.mockReturnValue(ok([proposal]));
    render(<ProposalList />);

    expect(screen.getByText("1,000")).toBeInTheDocument();
    expect(screen.getByText("Raise the deposit cap")).toBeInTheDocument();
  });

  // Zero votes is a real tally. Rendering it as unavailable would repeat the vault defect.
  it("renders a zero tally as zero, not as unavailable", () => {
    mocks.useProposals.mockReturnValue(ok([proposal]));
    render(<ProposalList />);

    expect(screen.getAllByText("0").length).toBeGreaterThan(0);
    expect(screen.queryByText("unavailable")).not.toBeInTheDocument();
  });
});
