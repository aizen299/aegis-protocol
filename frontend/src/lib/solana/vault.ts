import { PublicKey, TransactionInstruction } from "@solana/web3.js";

import vaultIdlJson from "@/idl/aegis_vault.json";
import { decodeAccount, instructionData, type Idl } from "./idl";

export const vaultIdl = vaultIdlJson as unknown as Idl;
export const VAULT_PROGRAM = new PublicKey(vaultIdl.address);
export const TOKEN_PROGRAM = new PublicKey("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA");
export const ASSOCIATED_TOKEN_PROGRAM = new PublicKey("ATokenGPvbdGVxr1b2hvZbsiqW5xWH25efTNsLJA8knL");
export const SYSTEM_PROGRAM = new PublicKey("11111111111111111111111111111111");

const pda = (seeds: (Buffer | Uint8Array)[], program = VAULT_PROGRAM) => PublicKey.findProgramAddressSync(seeds, program)[0];
const text = (s: string) => new TextEncoder().encode(s);

export const vaultAddress = (mint: PublicKey) => pda([text("vault"), mint.toBytes()]);
export const vaultTokensAddress = (vault: PublicKey) => pda([text("tokens"), vault.toBytes()]);
export const positionAddress = (vault: PublicKey, owner: PublicKey) => pda([text("position"), vault.toBytes(), owner.toBytes()]);
export const eventAuthority = () => pda([text("__event_authority")]);
export const associatedTokenAddress = (owner: PublicKey, mint: PublicKey) =>
  pda([owner.toBytes(), TOKEN_PROGRAM.toBytes(), mint.toBytes()], ASSOCIATED_TOKEN_PROGRAM);

export type VaultState = {
  mint: PublicKey;
  totalShares: bigint;
  depositCap: bigint;
  minDeposit: bigint;
  paused: boolean;
  withdrawalsFrozen: boolean;
  shareOffset: number;
};

export function decodeVault(data: Uint8Array): VaultState {
  const v = decodeAccount(vaultIdl, "Vault", data);
  return {
    mint: v.mint as PublicKey,
    totalShares: v.total_shares as bigint,
    depositCap: v.deposit_cap as bigint,
    minDeposit: v.min_deposit as bigint,
    paused: v.paused as boolean,
    withdrawalsFrozen: v.withdrawals_frozen as boolean,
    shareOffset: v.share_offset as number,
  };
}

export function decodePositionShares(data: Uint8Array): bigint {
  return decodeAccount(vaultIdl, "Position", data).shares as bigint;
}

// The program's own conversions: floor both ways, one virtual asset and 10^offset virtual shares.
// solana/programs/aegis_vault/src/math.rs.
export function convertToShares(assets: bigint, totalAssets: bigint, totalShares: bigint, offset: number): bigint {
  return (assets * (totalShares + 10n ** BigInt(offset))) / (totalAssets + 1n);
}

export function convertToAssets(shares: bigint, totalAssets: bigint, totalShares: bigint, offset: number): bigint {
  return (shares * (totalAssets + 1n)) / (totalShares + 10n ** BigInt(offset));
}

// Accounts in the IDL's order for the instruction, so a reordering there fails a test here.
function accounts(name: string, byName: Record<string, { pubkey: PublicKey; isSigner: boolean; isWritable: boolean }>) {
  const ix = vaultIdl.instructions.find((i) => i.name === name)!;
  return ix.accounts.map((a) => {
    const meta = byName[a.name];
    if (!meta) throw new Error(`${name}: no account given for ${a.name}`);
    return meta;
  });
}

const w = (pubkey: PublicKey, isSigner = false) => ({ pubkey, isSigner, isWritable: true });
const r = (pubkey: PublicKey) => ({ pubkey, isSigner: false, isWritable: false });

export function depositInstruction(p: { mint: PublicKey; depositor: PublicKey; amount: bigint; minShares: bigint }): TransactionInstruction {
  const vault = vaultAddress(p.mint);
  return new TransactionInstruction({
    programId: VAULT_PROGRAM,
    data: Buffer.from(instructionData(vaultIdl, "deposit", { amount: p.amount, min_shares: p.minShares })),
    keys: accounts("deposit", {
      depositor: w(p.depositor, true),
      receiver: r(p.depositor),
      vault: w(vault),
      vault_tokens: w(vaultTokensAddress(vault)),
      depositor_tokens: w(associatedTokenAddress(p.depositor, p.mint)),
      position: w(positionAddress(vault, p.depositor)),
      token_program: r(TOKEN_PROGRAM),
      system_program: r(SYSTEM_PROGRAM),
      event_authority: r(eventAuthority()),
      program: r(VAULT_PROGRAM),
    }),
  });
}

export function withdrawInstruction(p: { mint: PublicKey; owner: PublicKey; shares: bigint; minAmount: bigint }): TransactionInstruction {
  const vault = vaultAddress(p.mint);
  return new TransactionInstruction({
    programId: VAULT_PROGRAM,
    data: Buffer.from(instructionData(vaultIdl, "withdraw", { shares: p.shares, min_amount: p.minAmount })),
    keys: accounts("withdraw", {
      owner: { pubkey: p.owner, isSigner: true, isWritable: false },
      vault: w(vault),
      vault_tokens: w(vaultTokensAddress(vault)),
      receiver_tokens: w(associatedTokenAddress(p.owner, p.mint)),
      position: w(positionAddress(vault, p.owner)),
      token_program: r(TOKEN_PROGRAM),
      event_authority: r(eventAuthority()),
      program: r(VAULT_PROGRAM),
    }),
  });
}
