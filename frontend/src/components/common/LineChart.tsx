"use client";

import { useId, useMemo, useRef, useState } from "react";

// A single-series history with a crosshair and tooltip. Values arrive already scaled for display;
// labels use text tokens, never the series colour. The table beside it is the non-visual view.
export type Point = { t: number; v: number; label: string };

export function LineChart({ points, title, height = 220 }: { points: Point[]; title: string; height?: number }) {
  const ref = useRef<SVGSVGElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const id = useId();
  const w = 720;
  const pad = { top: 12, right: 12, bottom: 16, left: 72 };

  const geom = useMemo(() => {
    if (points.length === 0) return null;
    const ts = points.map((p) => p.t);
    const vs = points.map((p) => p.v);
    const tMin = Math.min(...ts);
    const tMax = Math.max(...ts);
    let vMin = Math.min(...vs);
    let vMax = Math.max(...vs);
    if (vMin === vMax) {
      vMin -= Math.abs(vMin) * 0.01 || 1;
      vMax += Math.abs(vMax) * 0.01 || 1;
    }
    const x = (t: number) => pad.left + ((t - tMin) / (tMax - tMin || 1)) * (w - pad.left - pad.right);
    const y = (v: number) => pad.top + (1 - (v - vMin) / (vMax - vMin)) * (height - pad.top - pad.bottom);
    const ticks = [0, 0.5, 1].map((f) => vMin + f * (vMax - vMin));
    return { x, y, ticks, d: points.map((p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)},${y(p.v).toFixed(1)}`).join(" ") };
  }, [points, height, pad.left, pad.right, pad.top, pad.bottom]);

  if (!geom) return null;

  function move(clientX: number) {
    const svg = ref.current;
    if (!svg) return;
    const rect = svg.getBoundingClientRect();
    const sx = ((clientX - rect.left) / rect.width) * w;
    let best = 0;
    for (let i = 1; i < points.length; i++) {
      if (Math.abs(geom!.x(points[i]!.t) - sx) < Math.abs(geom!.x(points[best]!.t) - sx)) best = i;
    }
    setHover(best);
  }

  const active = hover === null ? null : points[hover]!;
  const fmt = (v: number) => v.toLocaleString(undefined, { maximumFractionDigits: 6 });
  // Axis ticks are for reading scale, not value: compact, so they never crowd the plot.
  const tick = (v: number) => new Intl.NumberFormat(undefined, { notation: "compact", maximumSignificantDigits: 5 }).format(v);

  return (
    <figure className="relative">
      <figcaption id={id} className="sr-only">
        {title}: {points.length} points from {points[0]!.label} to {points[points.length - 1]!.label}
      </figcaption>
      <svg
        ref={ref}
        viewBox={`0 0 ${w} ${height}`}
        className="w-full touch-none"
        role="img"
        aria-labelledby={id}
        onPointerMove={(e) => move(e.clientX)}
        onPointerLeave={() => setHover(null)}
      >
        {geom.ticks.map((v) => (
          <g key={v}>
            <line x1={pad.left} x2={w - pad.right} y1={geom.y(v)} y2={geom.y(v)} className="stroke-border" strokeWidth={1} />
            <text x={pad.left - 8} y={geom.y(v)} textAnchor="end" dominantBaseline="middle" className="fill-muted-foreground text-[12px] tabular">
              {tick(v)}
            </text>
          </g>
        ))}
        <path d={geom.d} className="fill-none stroke-chart" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
        {active ? (
          <>
            <line x1={geom.x(active.t)} x2={geom.x(active.t)} y1={pad.top} y2={height - pad.bottom} className="stroke-muted-foreground/60" strokeWidth={1} strokeDasharray="3 3" />
            <circle cx={geom.x(active.t)} cy={geom.y(active.v)} r={4.5} className="fill-chart stroke-card" strokeWidth={2} />
          </>
        ) : null}
        <rect x={pad.left} y={pad.top} width={w - pad.left - pad.right} height={height - pad.top - pad.bottom} fill="transparent" />
      </svg>
      {active ? (
        <div
          className="pointer-events-none absolute top-2 rounded-md border bg-popover px-2.5 py-1.5 text-xs shadow-md"
          style={{ left: `clamp(0px, calc(${(geom.x(active.t) / w) * 100}% - 60px), calc(100% - 140px))` }}
          role="status"
        >
          <p className="tabular font-mono font-medium text-foreground">{fmt(active.v)}</p>
          <p className="text-muted-foreground">{active.label}</p>
        </div>
      ) : null}
    </figure>
  );
}
