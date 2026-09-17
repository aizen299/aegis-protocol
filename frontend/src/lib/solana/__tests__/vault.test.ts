// @vitest-environment node
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { PublicKey } from "@solana/web3.js";
import { describe, expect, it } from "vitest";

import { decodeAccount, instructionData } from "../idl";
import {
  associatedTokenAddress,
  convertToAssets,
  convertToShares,
  decodeVault,
  depositInstruction,
  positionAddress,
  VAULT_PROGRAM,
  vaultAddress,
  vaultIdl,
  vaultTokensAddress,
  withdrawInstruction,
} from "../vault";

const mint = new PublicKey(new Uint8Array(32).fill(1));
const owner = new PublicKey(new Uint8Array(32).fill(2));

const le = (n: bigint, width: number) => Array.from({ length: width }, (_, i) => Number((n >> BigInt(8 * i)) & 0xffn));

describe("vault instruction data", () => {
  // Built here from Anchor's rule, not from the IDL's discriminator, so a wrong IDL fails.
  it("is sha256('global:<name>')[..8] followed by the arguments little-endian", () => {
    const data = instructionData(vaultIdl, "deposit", { amount: 1_500_000n, min_shares: 1n << 70n });
    const disc = [...createHash("sha256").update("global:deposit").digest().subarray(0, 8)];
    expect([...data]).toEqual([...disc, ...le(1_500_000n, 8), ...le(1n << 70n, 16)]);

    const w = instructionData(vaultIdl, "withdraw", { shares: 7n, min_amount: 5n });
    expect([...w.subarray(0, 8)]).toEqual([...createHash("sha256").update("global:withdraw").digest().subarray(0, 8)]);
    expect(w.length).toBe(8 + 16 + 8);
  });

  it("refuses values that do not fit, and missing or unknown arguments", () => {
    expect(() => instructionData(vaultIdl, "deposit", { amount: 1n << 64n, min_shares: 0n })).toThrow(/fit u64/);
    expect(() => instructionData(vaultIdl, "deposit", { amount: -1n, min_shares: 0n })).toThrow(/fit u64/);
    expect(() => instructionData(vaultIdl, "deposit", { amount: 1n })).toThrow(/min_shares/);
    expect(() => instructionData(vaultIdl, "deposit", { amount: 1n, min_shares: 0n, extra: 1n })).toThrow(/unknown/);
  });
});

// Vectors from the Go backend's own derivation (internal/chain/svm), an independent implementation.
describe("vault addresses", () => {
  it("derive as the backend derives them", () => {
    expect(VAULT_PROGRAM.toBase58()).toBe("h5VEwZjPpX6zug14r44i4QzpPzYBKeAHQYa5QZdzyGo");
    const vault = vaultAddress(mint);
    expect(vault.toBase58()).toBe("6ufDPnyHNxWeqDmnkXH4X8wStgjniArFtKiR6FZk2VF4");
    expect(vaultTokensAddress(vault).toBase58()).toBe("sUgsMFA6W5pZ6GC96tLQuxM7puYNVVisv1LBScqD11P");
    expect(positionAddress(vault, owner).toBase58()).toBe("HHThX2Dbxa21AmwrF2GSeQB9w3m4Zo9SxmAJoZ3nmXop");
    expect(associatedTokenAddress(owner, mint).toBase58()).toBe("9SBAq6YVfq1ECthq7yBBLdGDoWnhwgDd7kSJ7eZREFDc");
  });
});

describe("vault instructions", () => {
  it.each([
    ["deposit", depositInstruction({ mint, depositor: owner, amount: 10n, minShares: 1n })],
    ["withdraw", withdrawInstruction({ mint, owner, shares: 10n, minAmount: 1n })],
  ] as const)("%s lists accounts in the IDL's order with its signer and writable flags", (name, ix) => {
    const idlIx = (vaultIdl as unknown as { instructions: { name: string; accounts: { name: string; writable?: boolean; signer?: boolean; address?: string }[] }[] })
      .instructions.find((i) => i.name === name)!;
    expect(ix.keys).toHaveLength(idlIx.accounts.length);
    idlIx.accounts.forEach((a, i) => {
      expect(ix.keys[i]!.isWritable, a.name).toBe(Boolean(a.writable));
      expect(ix.keys[i]!.isSigner, a.name).toBe(Boolean(a.signer));
      if (a.address) expect(ix.keys[i]!.pubkey.toBase58(), a.name).toBe(a.address);
    });
    expect(ix.programId.equals(VAULT_PROGRAM)).toBe(true);
  });
});

describe("vault account decoding", () => {
  const layouts = JSON.parse(readFileSync(resolve(process.cwd(), "../solana/layouts/aegis_vault.json"), "utf8")) as Record<
    string,
    { discriminator: number[]; size: number; fields: { name: string; offset: number; size: number }[] }
  >;

  // The account is laid out from the committed layout baseline, independently of the IDL decoder.
  it("reads fields at the offsets the layout baseline records", () => {
    const layout = layouts.Vault!;
    const data = new Uint8Array(layout.size);
    data.set(layout.discriminator, 0);
    const at = (name: string) => layout.fields.find((f) => f.name === name)!.offset;
    data.set(mint.toBytes(), at("mint"));
    data.set(le(123_456_789_000n, 16), at("total_shares"));
    data.set(le(5_000_000n, 8), at("deposit_cap"));
    data.set(le(1_000n, 8), at("min_deposit"));
    data[at("paused")] = 1;
    data[at("withdrawals_frozen")] = 0;
    data[at("share_offset")] = 3;

    expect(decodeVault(data)).toEqual({
      mint,
      totalShares: 123_456_789_000n,
      depositCap: 5_000_000n,
      minDeposit: 1_000n,
      paused: true,
      withdrawalsFrozen: false,
      shareOffset: 3,
    });
  });

  it("refuses another account's data", () => {
    const position = layouts.Position!;
    const data = new Uint8Array(layouts.Vault!.size);
    data.set(position.discriminator, 0);
    expect(() => decodeVault(data)).toThrow(/not a Vault/);
    expect(() => decodeAccount(vaultIdl, "Vault", new Uint8Array(12).fill(0))).toThrow();
  });
});

// Mirrors solana/programs/aegis_vault/src/math.rs and its tests.
describe("share conversions", () => {
  it("mints the first deposit at the offset", () => {
    expect(convertToShares(1_000_000n, 0n, 0n, 3)).toBe(1_000_000_000n);
  });

  it("never returns more on a round trip than was deposited", () => {
    const cases: [bigint, bigint, bigint][] = [
      [1n, 0n, 0n],
      [999n, 1_000_001n, 1_000_000_000n],
      [123_456_789n, 987_654_321n, 555_555_555_555n],
    ];
    for (const [amount, assets, shares] of cases) {
      const minted = convertToShares(amount, assets, shares, 3);
      expect(convertToAssets(minted, assets + amount, shares + minted, 3) <= amount).toBe(true);
    }
  });

  it("floors, as the program does", () => {
    expect(convertToAssets(1_500n, 1n, 1_000n, 3)).toBe(1n);
    expect(convertToShares(1n, 2n, 0n, 3)).toBe(333n);
  });
});
