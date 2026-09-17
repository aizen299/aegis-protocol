"use client";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useApiHealth } from "@/lib/queries";
import { cn } from "@/lib/utils";

export function ApiHealth() {
  const health = useApiHealth();
  const state = health.isPending ? "checking" : health.data ? "online" : "unreachable";
  const label = { checking: "Checking the API", online: "API online", unreachable: "API unreachable" }[state];

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span role="status" aria-label={label} className="flex h-9 items-center gap-2 rounded-md px-1.5 text-xs text-muted-foreground sm:px-2">
          <span
            aria-hidden="true"
            className={cn(
              "size-2 rounded-full",
              state === "online" && "bg-success",
              state === "unreachable" && "bg-destructive",
              state === "checking" && "animate-pulse-dot bg-muted-foreground",
            )}
          />
          <span className="hidden lg:inline">{state === "online" ? "Live" : state === "unreachable" ? "Offline" : "…"}</span>
        </span>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}
