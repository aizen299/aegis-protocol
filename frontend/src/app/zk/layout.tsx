import type { ReactNode } from "react";

import { SubNav } from "@/components/SubNav";

export default function ZkLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-col gap-5">
      <SubNav
        links={[
          { href: "/zk", label: "Overview" },
          { href: "/zk/commitments", label: "Commitments" },
          { href: "/zk/actions", label: "Executed actions" },
        ]}
      />
      {children}
    </div>
  );
}
