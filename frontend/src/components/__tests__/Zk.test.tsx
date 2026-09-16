import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  useAnonymitySet: vi.fn(),
  useCommitments: vi.fn(),
  useNullifierStatus: vi.fn(),
}));

vi.mock("@/lib/queries", () => ({
  useAnonymitySet: mocks.useAnonymitySet,
  useCommitments: mocks.useCommitments,
  useNullifierStatus: mocks.useNullifierStatus,
}));

import { AnonymitySet } from "../zk/AnonymitySet";
import { CommitmentList } from "../zk/CommitmentList";
import { NullifierLookup } from "../zk/NullifierLookup";

const ok = <T,>(data: T) => ({ data, isPending: false, isError: false, error: null });
const pending = { data: undefined, isPending: true, isError: false, error: null };
const failed = { data: undefined, isPending: false, isError: true, error: { message: "no api" } };

const set = (leafCount: number) =>
  ok({ chainId: 31337, tree: "0xtree", leafCount, currentRoot: "0xroot" });

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("AnonymitySet", () => {
  // docs/v0.4-zk-plan.md §4: the limitation is surfaced, not left to be inferred from a number.
  it("warns in words when a single commitment provides no anonymity", () => {
    mocks.useAnonymitySet.mockReturnValue(set(1));
    render(<AnonymitySet />);

    expect(screen.getByRole("status")).toHaveTextContent(/no anonymity/i);
    expect(screen.getByText("1")).toBeInTheDocument();
  });

  it("still names the count for a larger set, without calling it private", () => {
    mocks.useAnonymitySet.mockReturnValue(set(250));
    render(<AnonymitySet />);

    const status = screen.getByRole("status");
    expect(status).toHaveTextContent("250");
    expect(status.textContent?.toLowerCase()).not.toMatch(/\bprivate\b/);
  });

  it("shows a failed read as a failure, not as an empty tree", () => {
    mocks.useAnonymitySet.mockReturnValue(failed);
    render(<AnonymitySet />);

    expect(screen.getByTestId("async-failed")).toBeInTheDocument();
    expect(screen.queryByText(/no anonymity/i)).not.toBeInTheDocument();
  });
});

describe("CommitmentList", () => {
  // The privacy property the schema enforces must not be undone by the UI.
  it("shows no depositor, and says why", () => {
    mocks.useCommitments.mockReturnValue(
      ok({
        chainId: 31337,
        limit: 100,
        offset: 0,
        count: 1,
        items: [
          {
            chainId: 31337,
            tree: "0xtree",
            leafIndex: 0,
            commitment: "0xcommitment000000000000000000000000000000000000000000000000000000",
            rootAfter: "0xroot0000000000000000000000000000000000000000000000000000000000",
            txHash: "0xtx",
            logIndex: 0,
            blockNumber: 4,
            insertedAt: "2026-09-16T00:00:00Z",
          },
        ],
      }),
    );
    render(<CommitmentList />);

    // Scoped to the column headers: the explanatory note below the table says "depositor" itself,
    // so a document-wide search would match the disclosure rather than the data.
    const headers = screen.getAllByRole("columnheader").map((th) => th.textContent ?? "");
    expect(headers).not.toEqual(
      expect.arrayContaining([expect.stringMatching(/depositor|owner|sender|address/i)]),
    );
    expect(headers).toEqual(expect.arrayContaining(["Leaf", "Commitment"]));

    expect(screen.getByText(/no depositor column/i)).toBeInTheDocument();
  });
});

describe("NullifierLookup", () => {
  it("rejects a malformed nullifier without querying", async () => {
    mocks.useNullifierStatus.mockReturnValue(pending);
    render(<NullifierLookup />);

    await userEvent.type(screen.getByLabelText(/check whether/i), "nonsense");

    expect(screen.getByRole("status")).toHaveTextContent(/64 lowercase hex/i);
    expect(screen.queryByTestId("nullifier-result")).not.toBeInTheDocument();
  });

  // A lookup that fails must not read as "not spent" — that is the answer a user would act on.
  it("does not report a failed lookup as unspent", async () => {
    mocks.useNullifierStatus.mockReturnValue(failed);
    render(<NullifierLookup />);

    await userEvent.type(screen.getByLabelText(/check whether/i), `0x${"a".repeat(64)}`);

    const result = screen.getByTestId("nullifier-result");
    expect(result).toHaveTextContent(/failure to read/i);
    expect(result).not.toHaveTextContent(/not spent/i);
  });

  it("reports a spent nullifier as spent", async () => {
    mocks.useNullifierStatus.mockReturnValue(
      ok({ chainId: 31337, gate: "0xgate", nullifier: `0x${"a".repeat(64)}`, spent: true }),
    );
    render(<NullifierLookup />);

    await userEvent.type(screen.getByLabelText(/check whether/i), `0x${"a".repeat(64)}`);
    expect(screen.getByTestId("nullifier-result")).toHaveTextContent(/spent/i);
  });
});
