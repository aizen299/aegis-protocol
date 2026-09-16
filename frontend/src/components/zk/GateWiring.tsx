"use client";

import { Async } from "@/components/Async";
import { Panel, Stat } from "@/components/Panel";
import { useZkGate } from "@/lib/queries";
import { value as valueOf } from "@/lib/readState";

export function GateWiring() {
  const gate = useZkGate();

  return (
    <Panel title="Gate">
      <Async query={gate} empty="No gate is deployed.">
        {(data) => (
          <>
            <Stat label="Gate" state={valueOf(data.address)} />
            <Stat label="Tree" state={valueOf(data.tree)} />
            <Stat label="Verifier" state={valueOf(data.verifier)} />
          </>
        )}
      </Async>
    </Panel>
  );
}
