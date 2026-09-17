// The one palette every component source renders in. globals.css declares these as CSS variables
// for Tailwind and shadcn/ui, and the MUI theme is built from the same values; a test holds the two
// in agreement. docs/v2.0-solana-plan.md §21.1.

export const palette = {
  dark: {
    background: "#0A0F1C",
    foreground: "#F1F5F9",
    card: "#111726",
    "card-foreground": "#F1F5F9",
    popover: "#111726",
    "popover-foreground": "#F1F5F9",
    primary: "#F59E0B",
    "primary-foreground": "#0F172A",
    secondary: "#1A2132",
    "secondary-foreground": "#E2E8F0",
    muted: "#1A2132",
    "muted-foreground": "#94A3B8",
    accent: "#1E263A",
    "accent-foreground": "#F1F5F9",
    destructive: "#DC2626",
    "destructive-foreground": "#FFFFFF",
    border: "#243047",
    input: "#243047",
    ring: "#F59E0B",
    success: "#10B981",
    warning: "#FBBF24",
    info: "#38BDF8",
    violet: "#8B5CF6",
    arbitrum: "#28A0F0",
    solana: "#14F195",
  },
  light: {
    background: "#F8FAFC",
    foreground: "#0F172A",
    card: "#FFFFFF",
    "card-foreground": "#0F172A",
    popover: "#FFFFFF",
    "popover-foreground": "#0F172A",
    primary: "#B45309",
    "primary-foreground": "#FFFFFF",
    secondary: "#F1F5F9",
    "secondary-foreground": "#0F172A",
    muted: "#F1F5F9",
    "muted-foreground": "#475569",
    accent: "#E2E8F0",
    "accent-foreground": "#0F172A",
    destructive: "#DC2626",
    "destructive-foreground": "#FFFFFF",
    border: "#E2E8F0",
    input: "#CBD5E1",
    ring: "#B45309",
    success: "#047857",
    warning: "#B45309",
    info: "#0369A1",
    violet: "#6D28D9",
    arbitrum: "#1B7FC4",
    solana: "#0B8F5A",
  },
} as const;

export type Mode = keyof typeof palette;
export type Token = keyof (typeof palette)["dark"];

export const radius = "0.625rem";

// "H S% L%", the form shadcn/ui's Tailwind config wraps in hsl(), so opacity modifiers work.
export function hexToHslTriplet(hex: string): string {
  const n = parseInt(hex.slice(1), 16);
  const r = ((n >> 16) & 255) / 255;
  const g = ((n >> 8) & 255) / 255;
  const b = (n & 255) / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  let h = 0;
  let s = 0;
  if (max !== min) {
    const d = max - min;
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
    if (max === r) h = (g - b) / d + (g < b ? 6 : 0);
    else if (max === g) h = (b - r) / d + 2;
    else h = (r - g) / d + 4;
    h *= 60;
  }
  return `${Math.round(h * 10) / 10} ${Math.round(s * 1000) / 10}% ${Math.round(l * 1000) / 10}%`;
}
