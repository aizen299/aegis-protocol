import { formatUnits, parseUnits } from "viem";

// Decimals are a property of the asset, never a protocol constant. Nothing here assumes 18.

// Returns undefined rather than a dash when it cannot render. The caller knows whether the value is
// missing because a read is in flight, because it failed, or because it is genuinely absent; a dash
// chosen here would erase that distinction before the caller ever saw it.
export function formatAmount(
  value: bigint | undefined,
  decimals: number | undefined,
  symbol?: string,
): string | undefined {
  if (value === undefined || decimals === undefined) return undefined;

  const formatted = Number(formatUnits(value, decimals)).toLocaleString(undefined, {
    maximumFractionDigits: Math.min(decimals, 6),
  });
  return symbol ? `${formatted} ${symbol}` : formatted;
}

export function parseAmount(input: string, decimals: number | undefined): bigint | null {
  if (!input.trim() || decimals === undefined) return null;
  try {
    const parsed = parseUnits(input, decimals);
    return parsed > 0n ? parsed : null;
  } catch {
    return null;
  }
}

// A share is scaled by the vault's virtual-shares offset relative to its asset, so rendering share
// amounts needs the asset's decimals plus that offset — not a guess.
export function shareDecimals(
  assetDecimals: number | undefined,
  offset: number | undefined,
): number | undefined {
  if (assetDecimals === undefined || offset === undefined) return undefined;
  return assetDecimals + offset;
}

// The API sends unscaled integers as strings — a uint256 is not a JS number and never becomes one.
// Parsing failure returns undefined rather than 0, so a malformed field reads as unavailable.
export function formatRaw(
  raw: string | undefined,
  decimals: number | undefined,
  symbol?: string,
): string | undefined {
  if (raw === undefined || decimals === undefined) return undefined;
  try {
    return formatAmount(BigInt(raw), decimals, symbol);
  } catch {
    return undefined;
  }
}
