import { cn } from "@/lib/utils";

// One series, so no legend: the card names it. The figure beside it carries the value; the line only
// shows direction, and says so to assistive technology.
export function Sparkline({ values, label, className }: { values: number[]; label: string; className?: string }) {
  if (values.length < 2) {
    return <div className={cn("h-10 w-full rounded bg-muted/40", className)} aria-hidden="true" />;
  }
  const w = 160;
  const h = 40;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const points = values.map((v, i) => [(i / (values.length - 1)) * w, h - 3 - ((v - min) / span) * (h - 6)] as const);
  const d = points.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  const trend = values[values.length - 1]! > values[0]! ? "up" : values[values.length - 1]! < values[0]! ? "down" : "flat";

  return (
    <svg
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      role="img"
      aria-label={`${label}: ${values.length} settled rounds, trending ${trend}`}
      className={cn("h-10 w-full overflow-visible", className)}
    >
      <path d={`${d} L${w},${h} L0,${h} Z`} className="fill-chart/10" />
      <path d={d} className="fill-none stroke-chart" strokeWidth={2} vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" />
      <circle cx={points[points.length - 1]![0]} cy={points[points.length - 1]![1]} r={3} className="fill-chart stroke-card" strokeWidth={2} vectorEffect="non-scaling-stroke" />
    </svg>
  );
}
