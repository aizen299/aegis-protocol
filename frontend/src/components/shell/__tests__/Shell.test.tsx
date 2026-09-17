import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { chainByName } from "@/lib/chains";
import { navigation, renderOnChain } from "@/test/navigation";
import { ModuleGate } from "../ModuleGate";
import { navFor } from "../nav";

vi.mock("@/lib/chainContext", async (original) => {
  const actual = await original<typeof import("@/lib/chainContext")>();
  const { servedChains } = await import("@/lib/chains");
  const served = servedChains("anvil,solana-localnet");
  return {
    ...actual,
    useServedChains: () => served,
    useChain: () => served.find((c) => c.name === navigation.search.get("chain")) ?? served[0],
  };
});

afterEach(() => {
  cleanup();
  navigation.search = new URLSearchParams();
});

describe("navigation per chain", () => {
  it("offers only the modules each chain has", () => {
    expect(navFor(chainByName("anvil")!).map((i) => i.label)).toEqual([
      "Vault", "Oracle feeds", "Oracle nodes", "Governance", "Privacy",
    ]);
    expect(navFor(chainByName("solana-localnet")!).map((i) => i.label)).toEqual([
      "Vault", "Oracle feeds", "Oracle nodes", "Received governance",
    ]);
  });
});

describe("ModuleGate", () => {
  it("explains a module the chain lacks and links to a chain that has it", () => {
    renderOnChain("solana-localnet");
    render(<ModuleGate module="governance" path="/governance"><p>proposals</p></ModuleGate>);

    expect(screen.queryByText("proposals")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /governor is not on Solana Localnet/ })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Open on Anvil/ })).toHaveAttribute("href", "/governance?chain=anvil");
  });

  it("renders the module where the chain has it", () => {
    renderOnChain("anvil");
    render(<ModuleGate module="governance" path="/governance"><p>proposals</p></ModuleGate>);
    expect(screen.getByText("proposals")).toBeInTheDocument();
    expect(screen.queryByTestId("module-unavailable")).not.toBeInTheDocument();
  });
});
