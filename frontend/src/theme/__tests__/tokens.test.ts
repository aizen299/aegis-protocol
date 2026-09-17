import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { describe, expect, it } from "vitest";

import { hexToHslTriplet, palette } from "../tokens";

const css = readFileSync(resolve(process.cwd(), "src/app/globals.css"), "utf8");

function block(selector: string): Map<string, string> {
  const start = css.indexOf(`${selector} {`);
  const body = css.slice(start, css.indexOf("}", start));
  return new Map([...body.matchAll(/--([\w-]+): ([^;]+);/g)].map((m) => [m[1]!, m[2]!.trim()]));
}

// Tailwind and shadcn/ui read globals.css, the MUI theme reads tokens.ts. They must be one palette.
describe("design tokens", () => {
  it.each([
    ["light", ":root"],
    ["dark", ".dark"],
  ] as const)("globals.css declares the %s palette from tokens.ts", (mode, selector) => {
    const declared = block(selector);
    const tokens = Object.entries(palette[mode]);
    expect(tokens.length).toBeGreaterThan(0);
    for (const [name, hex] of tokens) expect(declared.get(name), name).toBe(hexToHslTriplet(hex));
    expect([...declared.keys()].filter((k) => k !== "radius").sort()).toEqual(tokens.map(([k]) => k).sort());
  });

  it("converts hex to the hsl triplet exactly", () => {
    expect(hexToHslTriplet("#FFFFFF")).toBe("0 0% 100%");
    expect(hexToHslTriplet("#000000")).toBe("0 0% 0%");
    expect(hexToHslTriplet("#FF0000")).toBe("0 100% 50%");
    expect(hexToHslTriplet("#F59E0B")).toBe("37.7 92.1% 50.2%");
  });
});
