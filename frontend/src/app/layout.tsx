import type { Metadata } from "next";
import type { ReactNode } from "react";

import { Nav } from "@/components/Nav";
import "./globals.css";
import { Providers } from "./providers";

export const metadata: Metadata = {
  title: "Aegis Protocol",
  description: "Vault, oracle, and governance",
};

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <Providers>
          <main className="mx-auto flex min-h-screen max-w-4xl flex-col gap-6 px-6 py-12">
            <header>
              <h1 className="text-xl font-semibold text-zinc-100">Aegis Protocol</h1>
              <p className="text-sm text-zinc-500">Vault &middot; Oracle &middot; Governance</p>
            </header>
            <Nav />
            {children}
          </main>
        </Providers>
      </body>
    </html>
  );
}
