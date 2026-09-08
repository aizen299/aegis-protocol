import { formatUnits, parseUnits } from "viem";

// Decimals are a property of the asset, never a protocol constant. Nothing here assumes 18.

export function formatAmount(
  value: bigint | undefined,
  decimals: number | undefined,
  symbol?: string,
): string {
  if (value === undefined || decimals === undefined) return "—";

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
