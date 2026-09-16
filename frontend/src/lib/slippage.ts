// The floor a caller puts under a fill.
//
// Share price moves with the strategy's reported holdings, so the rate seen when a transaction is
// built is not necessarily the rate it executes at. The contract takes a minimum and reverts below
// it; this computes that minimum, and the UI shows it rather than hiding it behind a default.

export const DEFAULT_TOLERANCE_BPS = 50; // 0.5%
export const MAX_TOLERANCE_BPS = 1000; // 10%

const BPS = 10_000n;

/// Returns the minimum acceptable output, rounded down.
///
/// Rounding down is deliberate: rounding up would set a floor above what the quoted rate produces,
/// so a transaction at exactly the expected price would revert.
export function minimumOut(expected: bigint | undefined, toleranceBps: number): bigint | undefined {
  if (expected === undefined) return undefined;
  if (!Number.isInteger(toleranceBps) || toleranceBps < 0 || toleranceBps > MAX_TOLERANCE_BPS) {
    return undefined;
  }

  return (expected * (BPS - BigInt(toleranceBps))) / BPS;
}

export function formatTolerance(toleranceBps: number): string {
  return `${(toleranceBps / 100).toFixed(2).replace(/\.?0+$/, "")}%`;
}
