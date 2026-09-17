"use client";

import { Async } from "@/components/Async";
import { Panel, Stat } from "@/components/Panel";
import { assessAnonymitySet } from "@/lib/anonymity";
import { useAnonymitySet } from "@/lib/queries";
import { value as valueOf } from "@/lib/readState";

const tones = {
  none: "border-destructive/40 bg-destructive/10 text-destructive",
  weak: "border-warning/40 bg-warning/10 text-warning",
  moderate: "border-border bg-muted/60 text-foreground",
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
