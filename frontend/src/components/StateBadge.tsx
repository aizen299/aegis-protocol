import { cn } from "@/lib/utils";

// Round and proposal states share a renderer: both are state machines whose current position is the
// first thing a reader needs, and both have terminal states that must not look like live ones.
const tone: Record<string, string> = {
  open: "info",
  quorum_met: "info",
  active: "info",
  pending: "neutral",
  settled: "success",
  succeeded: "success",
  executed: "success",
  queued: "warning",
  dispatched: "violet",
  failed: "danger",
  defeated: "danger",
  cancelled: "danger",
  expired: "danger",
};

const tones: Record<string, string> = {
  info: "border-info/30 bg-info/10 text-info",
  success: "border-success/30 bg-success/10 text-success",
  warning: "border-warning/30 bg-warning/10 text-warning",
  danger: "border-destructive/30 bg-destructive/10 text-destructive",
  violet: "border-violet/30 bg-violet/10 text-violet",
  neutral: "border-border bg-muted text-muted-foreground",
};

export function StateBadge({ state }: { state: string }) {
  const key = state.toLowerCase();
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border px-2 py-0.5 text-[11px] font-medium uppercase tracking-wide",
        tones[tone[key] ?? "neutral"],
      )}
    >
      <span aria-hidden="true" className="size-1.5 rounded-full bg-current" />
      {state.replace(/_/g, " ")}
    </span>
  );
}
