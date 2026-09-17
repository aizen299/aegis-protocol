import type { Family } from "@/lib/chains";
import { cn } from "@/lib/utils";

// Each family's mark in its own brand colour. Colour is never the only signal: a label accompanies it.
export function ChainMark({ family, className }: { family: Family; className?: string }) {
  if (family === "solana") {
    return (
      <svg viewBox="0 0 24 24" aria-hidden="true" className={cn("size-4 text-solana", className)}>
        <path fill="currentColor" d="M5.3 16.4a.7.7 0 0 1 .5-.2h15.5c.3 0 .5.4.2.6l-3 3a.7.7 0 0 1-.5.2H2.5c-.3 0-.5-.4-.2-.6l3-3Zm0-12.2a.7.7 0 0 1 .5-.2h15.5c.3 0 .5.4.2.6l-3 3a.7.7 0 0 1-.5.2H2.5c-.3 0-.5-.4-.2-.6l3-3Zm13.2 6a.7.7 0 0 0-.5-.2H2.5c-.3 0-.5.4-.2.6l3 3c.1.1.3.2.5.2h15.5c.3 0 .5-.4.2-.6l-3-3Z" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className={cn("size-4 text-arbitrum", className)}>
      <path fill="currentColor" d="M12 2 3.3 7v10L12 22l8.7-5V7L12 2Zm0 2.3 6.7 3.9v7.6L12 19.7l-6.7-3.9V8.2L12 4.3Z" />
      <path fill="currentColor" d="m13.4 7.6 3.9 9.6-1.9 1.1-3.3-8.2 1.3-2.5Zm-2.6 2.9 2.8 6.9-1.6.9-2.4-5.9 1.2-1.9Z" />
    </svg>
  );
}
