export function shortAddress(address: string): string {
  return address.length > 12 ? `${address.slice(0, 6)}…${address.slice(-4)}` : address;
}

export function shortId(id: string): string {
  return id.length > 14 ? `${id.slice(0, 10)}…${id.slice(-4)}` : id;
}

export function formatTime(iso: string | undefined): string {
  if (!iso) return "—";
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString();
}

// Seconds, because a chain's block time is not a clock. See docs/v1.0-production-plan.md §2.2.
export function formatUnixTime(seconds: number | undefined): string {
  if (seconds === undefined || seconds <= 0) return "—";
  return new Date(seconds * 1000).toLocaleString();
}

export function relativeToNow(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "—";
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return "—";

  const delta = Math.round((then - now) / 1000);
  const past = delta < 0;
  const magnitude = Math.abs(delta);

  const [value, unit] =
    magnitude < 60
      ? [magnitude, "second"]
      : magnitude < 3600
        ? [Math.round(magnitude / 60), "minute"]
        : magnitude < 86400
          ? [Math.round(magnitude / 3600), "hour"]
          : [Math.round(magnitude / 86400), "day"];

  const plural = value === 1 ? unit : `${unit}s`;
  return past ? `${value} ${plural} ago` : `in ${value} ${plural}`;
}
