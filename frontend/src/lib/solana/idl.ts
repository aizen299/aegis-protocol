import { PublicKey } from "@solana/web3.js";

// Encodes instruction data and decodes accounts from an Anchor IDL, for the fixed-size types this
// protocol's programs use. The IDL is the contract: frontend/src/idl is checked against solana/idl.

export type IdlType = string | { array: [IdlType, number] } | { defined: { name: string } };
type IdlField = { name: string; type: IdlType };

export type Idl = {
  address: string;
  instructions: { name: string; discriminator: number[]; args: IdlField[]; accounts: { name: string }[] }[];
  accounts: { name: string; discriminator: number[] }[];
  types: { name: string; type: { kind: string; fields?: IdlField[] } }[];
};

export type ArgValue = bigint | number | boolean | PublicKey;

const widths: Record<string, number> = { u8: 1, i8: 1, u16: 2, i16: 2, u32: 4, i32: 4, u64: 8, i64: 8, u128: 16, i128: 16 };

function encodeValue(type: IdlType, value: ArgValue): number[] {
  if (type === "bool") return [value ? 1 : 0];
  if (type === "pubkey") {
    if (!(value instanceof PublicKey)) throw new Error("pubkey argument is not a PublicKey");
    return [...value.toBytes()];
  }
  if (typeof type === "string" && type in widths) {
    const width = widths[type]!;
    const signed = type.startsWith("i");
    let n = BigInt(value as bigint | number);
    const bits = BigInt(width * 8);
    const min = signed ? -(1n << (bits - 1n)) : 0n;
    const max = signed ? (1n << (bits - 1n)) - 1n : (1n << bits) - 1n;
    if (n < min || n > max) throw new Error(`${n} does not fit ${type}`);
    if (n < 0n) n += 1n << bits;
    const out: number[] = [];
    for (let i = 0; i < width; i++) {
      out.push(Number(n & 0xffn));
      n >>= 8n;
    }
    return out;
  }
  throw new Error(`argument type ${JSON.stringify(type)} is not supported`);
}

export function instructionData(idl: Idl, name: string, args: Record<string, ArgValue>): Uint8Array {
  const ix = idl.instructions.find((i) => i.name === name);
  if (!ix) throw new Error(`the IDL has no instruction ${name}`);
  const bytes = [...ix.discriminator];
  for (const arg of ix.args) {
    if (!(arg.name in args)) throw new Error(`${name}: missing argument ${arg.name}`);
    bytes.push(...encodeValue(arg.type, args[arg.name]!));
  }
  const extra = Object.keys(args).filter((k) => !ix.args.some((a) => a.name === k));
  if (extra.length) throw new Error(`${name}: unknown arguments ${extra.join(", ")}`);
  return Uint8Array.from(bytes);
}

export type Decoded = Record<string, bigint | number | boolean | PublicKey | Uint8Array>;

// Refuses data whose discriminator is not the account's: a different account read as this one would
// otherwise decode into plausible nonsense.
export function decodeAccount(idl: Idl, name: string, data: Uint8Array): Decoded {
  const account = idl.accounts.find((a) => a.name === name);
  const fields = idl.types.find((t) => t.name === name)?.type.fields;
  if (!account || !fields) throw new Error(`the IDL has no account ${name}`);
  if (data.length < 8 || account.discriminator.some((b, i) => data[i] !== b)) {
    throw new Error(`this is not a ${name} account`);
  }
  let offset = 8;
  const out: Decoded = {};
  const take = (n: number) => {
    if (offset + n > data.length) throw new Error(`${name} account is truncated`);
    const slice = data.subarray(offset, offset + n);
    offset += n;
    return slice;
  };
  for (const field of fields) {
    const t = field.type;
    if (t === "bool") out[field.name] = take(1)[0] === 1;
    else if (t === "pubkey") out[field.name] = new PublicKey(take(32));
    else if (typeof t === "string" && t in widths) {
      const width = widths[t]!;
      const b = take(width);
      let n = 0n;
      for (let i = width - 1; i >= 0; i--) n = (n << 8n) | BigInt(b[i]!);
      if (t.startsWith("i") && n >= 1n << BigInt(width * 8 - 1)) n -= 1n << BigInt(width * 8);
      out[field.name] = width <= 4 && !t.startsWith("i") ? Number(n) : n;
    } else if (typeof t === "object" && "array" in t && t.array[0] === "u8") {
      out[field.name] = take(t.array[1]);
    } else {
      throw new Error(`${name}.${field.name}: type ${JSON.stringify(t)} is not supported`);
    }
  }
  return out;
}
