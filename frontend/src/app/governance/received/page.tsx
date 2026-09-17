import type { Metadata } from "next";

import { RemoteActionList } from "@/components/governance/RemoteActionList";

export const metadata: Metadata = { title: "Received governance" };

export default function ReceivedGovernancePage() {
  return <RemoteActionList />;
}
