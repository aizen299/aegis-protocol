import { PageHeader } from "@/components/shell/PageHeader";
import { ActionList } from "@/components/zk/ActionList";
import { AnonymitySet } from "@/components/zk/AnonymitySet";
import { GateWiring } from "@/components/zk/GateWiring";
import { NullifierLookup } from "@/components/zk/NullifierLookup";
import { ProveAction } from "@/components/zk/ProveAction";

export default function ZkPage() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Privacy"
        description="Deposit a commitment, then act without revealing which deposit is yours. Proofs are generated in this browser; the secret never leaves it."
      />
      <div className="grid gap-4 lg:grid-cols-[1.3fr_1fr]">
        <div className="flex min-w-0 flex-col gap-4">
          <ProveAction />
          <ActionList />
        </div>
        <div className="flex min-w-0 flex-col gap-4">
          <AnonymitySet />
          <NullifierLookup />
          <GateWiring />
        </div>
      </div>
    </div>
  );
}
