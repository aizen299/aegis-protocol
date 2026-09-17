import { createTheme } from "@mui/material/styles";

import { palette, radius, type Mode } from "./tokens";

// Built from the same tokens as globals.css, so a Material component renders in the app's palette.
export function muiTheme(mode: Mode) {
  const p = palette[mode];
  return createTheme({
    palette: {
      mode,
      primary: { main: p.primary, contrastText: p["primary-foreground"] },
      secondary: { main: p.violet },
      error: { main: p.destructive },
      warning: { main: p.warning },
      info: { main: p.info },
      success: { main: p.success },
      background: { default: p.background, paper: p.card },
      text: { primary: p.foreground, secondary: p["muted-foreground"] },
      divider: p.border,
    },
    shape: { borderRadius: parseFloat(radius) * 16 },
    typography: {
      fontFamily: "var(--font-inter), system-ui, sans-serif",
      fontSize: 14,
    },
    components: {
      MuiPaper: { styleOverrides: { root: { backgroundImage: "none", border: `1px solid ${p.border}` } } },
    },
  });
}
