import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  reads: {} as Record<string, unknown>,
  useAccount: vi.fn(),
  useGovernor: vi.fn(),
}));

vi.mock("wagmi", () => ({
  useAccount: mocks.useAccount,
  useBlock: () => ({ data: { timestamp: BigInt(Math.floor(Date.now() / 1000)) } }),
  useReadContract: ({ functionName }: { functionName: string }) => ({
    data: mocks.reads[functionName],
  }),
  useWriteContract: () => ({
    writeContract: vi.fn(),
    reset: vi.fn(),
    data: undefined,
    error: null,
    isPending: false,
  }),
  useWaitForTransactionReceipt: () => ({ data: undefined, error: null, isLoading: false }),
}));

vi.mock("@/lib/queries", () => ({ useGovernor: mocks.useGovernor }));

import { ProposalState, ZERO_ADDRESS } from "@/lib/governance";
import { ProposalActions } from "../governance/ProposalActions";

const account = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266";

beforeEach(() => {
  mocks.useAccount.mockReturnValue({ address: account, isConnected: true });
  mocks.useGovernor.mockReturnValue({
    data: {
      chainId: 31337,
      address: "0x322813Fd9A801c5507c9de605d63CEA4f2CE6c44",
      token: "0x68B1D87F95878fE05B998F19b66F4baba5De1aed",
      timelock: "0x59b670e9fA9D0A427751Af201D676719a970857b",
      tokenDecimals: 18,
    },
  });
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  mocks.reads = {};
});

function renderActive() {
  render(<ProposalActions proposalId="1" voteStart={1_789_000_000} />);
}

const voteButtons = () => ["For", "Against", "Abstain"].map((n) => screen.getByRole("button", { name: n }));

describe("ProposalActions", () => {
  // The §2.4 trap, at the component: tokens held, never delegated, and the buttons must not invite a
  // transaction that reverts with NoVotingPower.
  it("disables voting with the reason when tokens were never delegated", () => {
    mocks.reads = {
      state: ProposalState.ACTIVE,
      hasVoted: false,
      getPastVotes: 0n,
      getVotes: 0n,
      balanceOf: 10n ** 21n,
      delegates: ZERO_ADDRESS,
    };
    renderActive();

    for (const button of voteButtons()) expect(button).toBeDisabled();
    expect(screen.getByTestId("vote-reason")).toHaveTextContent(/never delegated/i);
    // The remedy is offered, framed as not rescuing this proposal.
    expect(screen.getByRole("button", { name: /delegate to myself — for future proposals/i })).toBeEnabled();
  });

  it("enables voting and shows the snapshot weight when the account can vote", () => {
    mocks.reads = {
      state: ProposalState.ACTIVE,
      hasVoted: false,
      getPastVotes: 5n * 10n ** 21n,
      getVotes: 5n * 10n ** 21n,
      balanceOf: 5n * 10n ** 21n,
      delegates: account,
    };
    renderActive();

    for (const button of voteButtons()) expect(button).toBeEnabled();
    expect(screen.getByTestId("vote-allowed")).toHaveTextContent("5,000");
    expect(screen.queryByRole("button", { name: /delegate to myself/i })).not.toBeInTheDocument();
  });

  // While the snapshot read is outstanding the buttons stay off: enabling them early is how a user
  // ends up signing a transaction that reverts.
  it("keeps voting disabled until the snapshot weight is known", () => {
    mocks.reads = { state: ProposalState.ACTIVE, hasVoted: false };
    renderActive();

    for (const button of voteButtons()) expect(button).toBeDisabled();
  });

  // The live defect, at the component: queued, delay not elapsed, and Execute must stay off.
  it("keeps Execute disabled while the timelock delay has not elapsed in chain time", () => {
    const executableAt = Math.floor(Date.now() / 1000) + 2 * 86_400;
    mocks.reads = { state: ProposalState.QUEUED, proposalOf: { executableAt } };
    renderActive();
    expect(screen.getByRole("button", { name: "Execute" })).toBeDisabled();
    expect(screen.getByText(/timelock delay has not elapsed/i)).toBeInTheDocument();
  });

  it("keeps Execute disabled while the executable time has not been read", () => {
    mocks.reads = { state: ProposalState.QUEUED };
    renderActive();
    expect(screen.getByRole("button", { name: "Execute" })).toBeDisabled();
  });

  it("offers queue only for a succeeded proposal", () => {
    mocks.reads = { state: ProposalState.SUCCEEDED };
    renderActive();
    expect(screen.getByRole("button", { name: /queue in the timelock/i })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Execute" })).toBeDisabled();
  });
});
