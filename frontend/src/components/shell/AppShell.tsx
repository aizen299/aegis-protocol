"use client";

import { Menu } from "lucide-react";
import { usePathname } from "next/navigation";
import { useState, type ReactNode } from "react";

import { TestWalletButton } from "@/components/TestWalletButton";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { TooltipProvider } from "@/components/ui/tooltip";
import { moduleOfPath, moduleRoots, useChain } from "@/lib/chainContext";
import { ApiHealth } from "./ApiHealth";
import { ChainSwitcher } from "./ChainSwitcher";
import { ModuleGate } from "./ModuleGate";
import { Brand, NavLinks, Sidebar } from "./Sidebar";
import { ThemeToggle } from "./ThemeToggle";

export function AppShell({ children }: { children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const chain = useChain();
  const section = moduleOfPath(usePathname());

  return (
    <TooltipProvider delayDuration={200}>
      <a
        href="#content"
        className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-50 focus:rounded-md focus:bg-primary focus:px-3 focus:py-2 focus:text-primary-foreground"
      >
        Skip to content
      </a>
      <div className="flex min-h-dvh">
        <Sidebar />
        <div className="flex min-w-0 flex-1 flex-col">
          <header className="sticky top-0 z-30 flex h-14 items-center gap-2 border-b bg-background/80 px-4 backdrop-blur supports-[backdrop-filter]:bg-background/60 md:px-6">
            <Sheet open={open} onOpenChange={setOpen}>
              <SheetTrigger asChild>
                <Button variant="ghost" size="icon" className="md:hidden" aria-label="Open navigation">
                  <Menu className="size-5" />
                </Button>
              </SheetTrigger>
              <SheetContent side="left" className="w-72 p-4">
                <SheetHeader className="mb-4 text-left">
                  <SheetTitle asChild>
                    <div>
                      <Brand />
                    </div>
                  </SheetTitle>
                </SheetHeader>
                <NavLinks onNavigate={() => setOpen(false)} />
              </SheetContent>
            </Sheet>
            <div className="hidden min-[420px]:block md:hidden">
              <Brand />
            </div>
            <div className="ml-auto flex min-w-0 items-center gap-1">
              <ApiHealth />
              <ThemeToggle />
              {chain.vm === "evm" ? <TestWalletButton /> : null}
              <ChainSwitcher />
            </div>
          </header>
          <main id="content" className="mx-auto flex w-full max-w-6xl flex-1 flex-col gap-6 px-4 py-6 md:px-8 md:py-8">
            <ModuleGate module={section} path={moduleRoots[section]}>
              {children}
            </ModuleGate>
          </main>
        </div>
      </div>
    </TooltipProvider>
  );
}
