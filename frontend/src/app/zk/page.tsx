import { AnonymitySet } from "@/components/zk/AnonymitySet";
import { ActionList } from "@/components/zk/ActionList";
import { GateWiring } from "@/components/zk/GateWiring";
import { NullifierLookup } from "@/components/zk/NullifierLookup";
import { ProveAction } from "@/components/zk/ProveAction";

export default function ZkPage() {
  return (
    <div className="flex flex-col gap-5">
      <AnonymitySet />
      <ProveAction />
      <GateWiring />
      <ActionList />
      <NullifierLookup />
    </div>
  );
}
