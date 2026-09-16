"use client";

import { Async } from "@/components/Async";
import { Panel, Stat } from "@/components/Panel";
import { assessAnonymitySet } from "@/lib/anonymity";
import { useAnonymitySet } from "@/lib/queries";
import { value as valueOf } from "@/lib/readState";

const tones = {
  none: "border-red-900/60 bg-red-950/40 text-red-200",
  weak: "border-amber-900/60 bg-amber-950/40 text-amber-200",
  moderate: "border-zinc-800 bg-zinc-900/60 text-zinc-300",
};

export function AnonymitySet() {
  const set = useAnonymitySet();

  return (
    <Panel title="Anonymity set">
      <Async query={set} empty="No tree is deployed.">
        {(data) => {
          const verdict = assessAnonymitySet(data.leafCount);

          return (
            <>
              <p className={`mb-4 rounded border px-4 py-3 text-sm ${tones[verdict.tone]}`} role="status">
                <strong className="block">{verdict.headline}</strong>
                {verdict.detail}
              </p>
              <Stat label="Commitments in the tree" state={valueOf(String(data.leafCount))} />
              <Stat label="Tree" state={valueOf(data.tree)} />
              {data.currentRoot ? (
                <Stat label="Current root" state={valueOf(data.currentRoot)} />
              ) : null}
            </>
          );
        }}
      </Async>
    </Panel>
  );
}
