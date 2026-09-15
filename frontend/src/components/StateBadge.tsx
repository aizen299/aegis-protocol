// Round and proposal states share a renderer: both are state machines whose current position is the
// first thing a reader needs, and both have terminal states that must not look like live ones.
const palettes: Record<string, string> = {
  open: "border-sky-900/60 bg-sky-950/40 text-sky-200",
  quorum_met: "border-sky-900/60 bg-sky-950/40 text-sky-200",
  active: "border-sky-900/60 bg-sky-950/40 text-sky-200",
  pending: "border-zinc-700 bg-zinc-900 text-zinc-300",
  settled: "border-emerald-900/60 bg-emerald-950/40 text-emerald-200",
  succeeded: "border-emerald-900/60 bg-emerald-950/40 text-emerald-200",
  executed: "border-emerald-900/60 bg-emerald-950/40 text-emerald-200",
  queued: "border-amber-900/60 bg-amber-950/40 text-amber-200",
  failed: "border-red-900/60 bg-red-950/40 text-red-200",
  defeated: "border-red-900/60 bg-red-950/40 text-red-200",
  cancelled: "border-red-900/60 bg-red-950/40 text-red-200",
  expired: "border-red-900/60 bg-red-950/40 text-red-200",
};

export function StateBadge({ state }: { state: string }) {
  const key = state.toLowerCase();
  const palette = palettes[key] ?? "border-zinc-700 bg-zinc-900 text-zinc-300";

  return (
    <span className={`rounded border px-2 py-0.5 font-mono text-xs uppercase ${palette}`}>
      {state.replace(/_/g, " ")}
    </span>
  );
}
