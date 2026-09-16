"use client";

import { useState } from "react";

import { Panel } from "@/components/Panel";
import { useNullifierStatus } from "@/lib/queries";

const shape = /^0x[0-9a-f]{64}$/;

export function NullifierLookup() {
  const [input, setInput] = useState("");
  const trimmed = input.trim().toLowerCase();
  const wellFormed = shape.test(trimmed);
  const status = useNullifierStatus(trimmed);

  return (
    <Panel title="Nullifier status">
      <label className="block text-sm text-zinc-500" htmlFor="nullifier">
        Check whether a nullifier has been spent
      </label>
      <input
        id="nullifier"
        value={input}
        onChange={(event) => setInput(event.target.value)}
        placeholder="0x…"
        spellCheck={false}
        className="mt-2 w-full rounded border border-edge bg-black/40 px-3 py-2 font-mono text-xs text-zinc-200"
      />

      {trimmed !== "" && !wellFormed ? (
        <p className="mt-3 text-sm text-amber-300" role="status">
          A nullifier is 0x followed by 64 lowercase hex characters.
        </p>
      ) : null}

      {wellFormed ? (
        <p className="mt-3 text-sm" role="status" data-testid="nullifier-result">
          {status.isPending ? (
            <span className="text-zinc-500">checking…</span>
          ) : status.isError || status.data === undefined ? (
            <span className="text-red-200">
              Could not check this nullifier. That is a failure to read, not a statement about
              whether it is spent.
            </span>
          ) : status.data.spent ? (
            <span className="text-zinc-200">
              Spent. This nullifier has been used and cannot be used again.
            </span>
          ) : (
            <span className="text-zinc-200">Not spent on this gate.</span>
          )}
        </p>
      ) : null}
    </Panel>
  );
}
