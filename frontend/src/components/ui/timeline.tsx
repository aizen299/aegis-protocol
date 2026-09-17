"use client";

// Adapted from the 21st.dev "Timeline" component by preetsuthar17: vertical only, statuses mapped to
// this app's tokens, a live step that pulses only without reduced motion, and a connector coloured by
// whether the step beneath it has been reached.

import { cva } from "class-variance-authority";
import { Check, Clock, Minus, X } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export type TimelineStatus = "completed" | "active" | "pending" | "error" | "skipped";

export type TimelineItem = {
  id: string;
  title: string;
  description?: ReactNode;
  timestamp?: string;
  status: TimelineStatus;
  icon?: ReactNode;
  content?: ReactNode;
};

const iconVariants = cva(
  "relative z-10 flex size-7 shrink-0 items-center justify-center rounded-full border-2 bg-card text-xs transition-colors duration-200",
  {
    variants: {
      status: {
        completed: "border-success bg-success text-background",
        active: "border-primary text-primary motion-safe:animate-pulse",
        pending: "border-border text-muted-foreground",
        error: "border-destructive bg-destructive text-destructive-foreground",
        skipped: "border-dashed border-border text-muted-foreground",
      },
    },
  },
);

const connectorVariants = cva("absolute left-[13px] top-8 h-[calc(100%-1.5rem)] w-0.5", {
  variants: {
    reached: { true: "bg-success/60", false: "bg-border" },
  },
});

const statusLabel: Record<TimelineStatus, string> = {
  completed: "done",
  active: "in progress",
  pending: "not yet",
  error: "stopped",
  skipped: "does not apply",
};

function statusIcon(status: TimelineStatus) {
  switch (status) {
    case "completed":
      return <Check className="size-3.5" strokeWidth={3} />;
    case "active":
    case "pending":
      return <Clock className="size-3.5" />;
    case "error":
      return <X className="size-3.5" strokeWidth={3} />;
    case "skipped":
      return <Minus className="size-3.5" />;
  }
}

export function Timeline({ items, className }: { items: TimelineItem[]; className?: string }) {
  return (
    <ol className={cn("relative flex flex-col", className)}>
      {items.map((item, index) => {
        const next = items[index + 1];
        return (
          <li key={item.id} className="relative flex gap-3 pb-6 last:pb-0">
            {next ? <div aria-hidden="true" className={connectorVariants({ reached: next.status === "completed" || next.status === "active" })} /> : null}
            <div className={iconVariants({ status: item.status })}>
              {item.icon ?? statusIcon(item.status)}
              <span className="sr-only">{statusLabel[item.status]}</span>
            </div>
            <div className="flex min-w-0 flex-1 flex-col gap-1 pt-0.5">
              <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
                <h3 className={cn("text-sm font-medium leading-tight", item.status === "pending" || item.status === "skipped" ? "text-muted-foreground" : "")}>
                  {item.title}
                </h3>
                {item.timestamp ? <time className="shrink-0 text-xs text-muted-foreground">{item.timestamp}</time> : null}
              </div>
              {item.description ? <div className="text-xs leading-relaxed text-muted-foreground">{item.description}</div> : null}
              {item.content ? <div className="mt-1.5">{item.content}</div> : null}
            </div>
          </li>
        );
      })}
    </ol>
  );
}
