import type { ReadState } from "@/lib/readState";

export function ReadValue({ state }: { state: ReadState }) {
  if (state.kind === "loading") {
    return (
      <span className="font-mono text-sm text-zinc-500" aria-busy="true" data-testid="stat-loading">
        loading…
      </span>
    );
  }

  if (state.kind === "failed") {
    return (
      <span
        className="font-mono text-sm text-amber-300"
        role="status"
        title={state.reason}
        data-testid="stat-failed"
      >
        unavailable
      </span>
    );
  }

  return (
    <span className="font-mono text-sm text-zinc-200" data-testid="stat-value">
      {state.text}
    </span>
  );
}
