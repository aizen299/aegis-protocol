// A contract read is in one of three states, and the UI has to tell them apart.
//
// Collapsing them into a single "no value" rendering is what let a total read failure display as an
// empty vault: an RPC outage, a wrong vault address, and a chain the vault is not deployed on all
// looked identical to nobody having deposited yet. See DEFERRED.md.

export type ReadState =
  | { kind: "loading" }
  | { kind: "failed"; reason: string }
  | { kind: "value"; text: string };

export const loading: ReadState = { kind: "loading" };

export function failed(reason?: string): ReadState {
  return { kind: "failed", reason: reason?.trim() || "the call did not return a value" };
}

export function value(text: string): ReadState {
  return { kind: "value", text };
}

// resolve turns a query's state plus a formatter into one of the three.
//
// isPending is checked before isError because a query that has never settled is loading even when a
// previous attempt failed; reporting it as failed would flicker an error on every refetch.
export function resolve(
  query: { isPending: boolean; isError: boolean; error?: { message?: string } | null },
  format: () => string | undefined,
): ReadState {
  if (query.isPending) return loading;
  if (query.isError) return failed(query.error?.message);

  const text = format();
  return text === undefined ? failed() : value(text);
}
