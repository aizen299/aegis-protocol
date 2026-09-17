import type { ReadState } from "@/lib/readState";

export function ReadValue({ state }: { state: ReadState }) {
  if (state.kind === "loading") {
    return (
      <span className="font-mono text-sm text-muted-foreground" aria-busy="true" data-testid="stat-loading">
        loading…
      </span>
    );
  }

  if (state.kind === "failed") {
    return (
      <span
        className="font-mono text-sm text-warning"
        role="status"
        title={state.reason}
        data-testid="stat-failed"
      >
        unavailable
      </span>
    );
  }

  return (
    <span className="min-w-0 break-all text-right font-mono text-sm text-foreground" data-testid="stat-value">
      {state.text}
    </span>
  );
}
