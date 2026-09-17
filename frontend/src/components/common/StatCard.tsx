import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { ReadValue } from "@/components/ReadValue";
import { Card } from "@/components/ui/card";
import type { ReadState } from "@/lib/readState";
import { cn } from "@/lib/utils";

export function StatCard({
  label,
  state,
  icon: Icon,
  hint,
  className,
}: {
  label: string;
  state: ReadState;
  icon?: LucideIcon;
  hint?: ReactNode;
  className?: string;
}) {
  return (
    <Card className={cn("flex flex-col gap-2 p-4 shadow-none", className)}>
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-medium uppercase tracking-wider text-muted-foreground">{label}</span>
        {Icon ? <Icon className="size-4 text-muted-foreground" aria-hidden="true" /> : null}
      </div>
      <div className="font-display text-xl font-semibold tracking-tight [&_span]:text-xl [&_span]:font-display">
        <ReadValue state={state} />
      </div>
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
    </Card>
  );
}
