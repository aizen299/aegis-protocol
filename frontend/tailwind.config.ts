import type { Config } from "tailwindcss";

export default {
  content: ["./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        surface: "#0b0d12",
        panel: "#141821",
        edge: "#232936",
      },
    },
  },
  plugins: [],
} satisfies Config;
