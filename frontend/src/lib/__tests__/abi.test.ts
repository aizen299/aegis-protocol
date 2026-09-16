import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

import { governorAbi, vaultEngineAbi } from "../abi";

// The frontend's ABIs are hand-written. The backend's are exported from the contracts and checked
// against `forge inspect` in Backend CI, so comparing against those ties these to the contracts
// transitively — without Frontend CI needing forge.
//
// A hand-written ABI that drifts still compiles and still type-checks. It fails at runtime, as a
// transaction that encodes the wrong selector. v1.2 changed deposit's signature, and this is the
// check that would have noticed a frontend that kept the old one.

type Entry = {
  type: string;
  name?: string;
  stateMutability?: string;
  inputs?: { type: string }[];
  outputs?: { type: string }[];
};

function committed(relative: string): Entry[] {
  const path = resolve(__dirname, "../../../../backend/pkg/contracts", relative);
  const abi = JSON.parse(readFileSync(path, "utf8")) as Entry[];
  // An empty or unreadable baseline must fail, never compare equal to nothing.
  expect(abi.length, `${relative} is empty`).toBeGreaterThan(0);
  return abi;
}

function signature(entry: Entry): string {
  return `${entry.name}(${(entry.inputs ?? []).map((i) => i.type).join(",")})`;
}

function assertMirrors(frontend: readonly Entry[], source: Entry[], label: string) {
  const functions = new Map(
    source.filter((e) => e.type === "function").map((e) => [signature(e), e]),
  );

  const checked = frontend.filter((e) => e.type === "function");
  expect(checked.length, `${label} declares no functions`).toBeGreaterThan(0);

  for (const entry of checked) {
    const sig = signature(entry);
    const match = functions.get(sig);
    expect(match, `${label}: ${sig} does not exist on the contract`).toBeDefined();
    expect(entry.stateMutability, `${label}: ${sig} mutability`).toBe(match?.stateMutability);
    expect(
      (entry.outputs ?? []).map((o) => o.type),
      `${label}: ${sig} outputs`,
    ).toEqual((match?.outputs ?? []).map((o) => o.type));
  }
}

describe("frontend ABIs mirror the contracts", () => {
  it("vaultEngineAbi", () => {
    assertMirrors(vaultEngineAbi, committed("vaultengine/VaultEngine.abi.json"), "vaultEngineAbi");
  });

  it("governorAbi", () => {
    assertMirrors(governorAbi, committed("governance/Governor.abi.json"), "governorAbi");
  });
});
