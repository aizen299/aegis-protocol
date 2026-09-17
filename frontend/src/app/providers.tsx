"use client";

import "@rainbow-me/rainbowkit/styles.css";

import { ThemeProvider as MuiThemeProvider } from "@mui/material/styles";
import { AppRouterCacheProvider } from "@mui/material-nextjs/v15-appRouter";
import { RainbowKitProvider, darkTheme, lightTheme } from "@rainbow-me/rainbowkit";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ThemeProvider, useTheme } from "next-themes";
import { useMemo, useState, type ReactNode } from "react";
import { WagmiProvider } from "wagmi";

import { Toaster } from "@/components/ui/sonner";
import { useMounted } from "@/hooks/useMounted";
import { SolanaProviders } from "@/lib/solana/SolanaProviders";
import { wagmiConfig } from "@/lib/wagmi";
import { palette } from "@/theme/tokens";
import { muiTheme } from "@/theme/mui";

function Themed({ children }: { children: ReactNode }) {
  const { resolvedTheme } = useTheme();
  const mode = useMounted() && resolvedTheme === "light" ? "light" : "dark";
  const mui = useMemo(() => muiTheme(mode), [mode]);
  const rainbow = mode === "light"
    ? lightTheme({ accentColor: palette.light.primary, borderRadius: "medium" })
    : darkTheme({ accentColor: palette.dark.primary, accentColorForeground: palette.dark["primary-foreground"], borderRadius: "medium" });

  return (
    <MuiThemeProvider theme={mui}>
      <RainbowKitProvider theme={rainbow}>
        {children}
        <Toaster richColors closeButton position="bottom-right" />
      </RainbowKitProvider>
    </MuiThemeProvider>
  );
}

export function Providers({ children }: { children: ReactNode }) {
  const [queryClient] = useState(() => new QueryClient());

  return (
    // Material's styles go in a cascade layer, so Tailwind's utilities win wherever both apply.
    <AppRouterCacheProvider options={{ enableCssLayer: true }}>
      <ThemeProvider attribute="class" defaultTheme="dark" enableSystem={false} disableTransitionOnChange>
        <WagmiProvider config={wagmiConfig}>
          <QueryClientProvider client={queryClient}>
            <SolanaProviders>
              <Themed>{children}</Themed>
            </SolanaProviders>
          </QueryClientProvider>
        </WagmiProvider>
      </ThemeProvider>
    </AppRouterCacheProvider>
  );
}
