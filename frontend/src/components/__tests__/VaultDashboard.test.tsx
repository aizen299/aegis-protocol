import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({
  useAccount: vi.fn(),
  useReadContract: vi.fn(),
  useReadContracts: vi.fn(),
}));

vi.mock("wagmi", () => ({
  useAccount: mocks.useAccount,
  useReadContract: mocks.useReadContract,
  useReadContracts: mocks.useReadContracts,
}));

vi.mock("@rainbow-me/rainbowkit", () => ({
  ConnectButton: () => <button type="button">Connect Wallet</button>,
}));

vi.mock("@/lib/env", () => ({
  env: {
    chainId: 31337,
    rpcUrl: "http://127.0.0.1:8545",
    apiUrl: "http://127.0.0.1:8090",
    vaultAddress: "0xCf7Ed3AccA5a467e9e704C703E8D87F634fB0Fc9",
    assetAddress: "0x5FbDB2315678afecb367f032d93F642f64180aa3",
    walletConnectProjectId: "",
  },
  requireVaultAddress: () => "0xCf7Ed3AccA5a467e9e704C703E8D87F634fB0Fc9",
}));

import { VaultDashboard } from "../VaultDashboard";

const pending = { data: undefined, isPending: true, isError: false, error: null };
const errored = (message: string) => ({
  data: undefined,
  isPending: false,
  isError: true,
  error: { message },
});
const ok = (data: unknown) => ({ data, isPending: false, isError: false, error: null });

// A 250 tUSD vault: six-decimal asset, three-decimal virtual-shares offset.
const vaultReads = ok([
  { result: 250_000_000n },
  { result: 250_000_000_000n },
  { result: 0n },
  { result: false },
  { result: false },
  { result: 3 },
  { result: "0x5FbDB2315678afecb367f032d93F642f64180aa3" },
]);
const assetReads = ok([{ result: 6 }, { result: "tUSD" }]);

beforeEach(() => {
  mocks.useAccount.mockReturnValue({ address: undefined, isConnected: false });
  mocks.useReadContract.mockReturnValue(pending);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("VaultDashboard", () => {
  it("renders vault figures scaled by the asset's own decimals", () => {
    mocks.useReadContracts.mockReturnValueOnce(vaultReads).mockReturnValueOnce(assetReads);
    render(<VaultDashboard />);

    expect(screen.getByText("250 tUSD")).toBeInTheDocument();
    expect(screen.getByText("250 shares")).toBeInTheDocument();
    expect(screen.getByText("uncapped")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  // The defect this module was written for. Observed against a local chain with no Multicall3
  // deployed: every read failed, and the dashboard showed a vault that looked merely empty.
  it("says the figures are unavailable when the vault read fails, rather than showing an empty vault", () => {
    mocks.useReadContracts
      .mockReturnValueOnce(errored("multicall3 not deployed"))
      .mockReturnValueOnce(pending);
    render(<VaultDashboard />);

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(/unavailable, not zero/i);
    expect(alert).toHaveTextContent(/multicall3 not deployed/);

    expect(screen.getAllByTestId("stat-failed")).toHaveLength(3);
    expect(screen.queryByTestId("stat-value")).not.toBeInTheDocument();
    // Specifically not the thing it used to show.
    expect(screen.queryByText("0 tUSD")).not.toBeInTheDocument();
  });

  it("shows reads in flight as loading rather than as failures", () => {
    mocks.useReadContracts.mockReturnValueOnce(pending).mockReturnValueOnce(pending);
    render(<VaultDashboard />);

    expect(screen.getAllByTestId("stat-loading").length).toBeGreaterThan(0);
    expect(screen.queryByTestId("stat-failed")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  // A vault with nothing in it is a real state and must not be reported as a failure.
  it("shows a genuinely empty vault as zero", () => {
    mocks.useReadContracts
      .mockReturnValueOnce(
        ok([
          { result: 0n },
          { result: 0n },
          { result: 0n },
          { result: false },
          { result: false },
          { result: 3 },
          { result: "0x5FbDB2315678afecb367f032d93F642f64180aa3" },
        ]),
      )
      .mockReturnValueOnce(assetReads);
    render(<VaultDashboard />);

    expect(screen.getByText("0 tUSD")).toBeInTheDocument();
    expect(screen.queryByTestId("stat-failed")).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("reports a failed asset read rather than rendering unscaled numbers", () => {
    mocks.useReadContracts
      .mockReturnValueOnce(vaultReads)
      .mockReturnValueOnce(errored("decimals() reverted"));
    render(<VaultDashboard />);

    expect(screen.getAllByTestId("stat-failed").length).toBeGreaterThan(0);
    expect(screen.queryByText(/250000000/)).not.toBeInTheDocument();
  });
});
