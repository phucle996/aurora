"use client";

import { useEffect, useState, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import { AlertTriangle } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { useUserSession } from "@/session/use-session";
import { ConsoleCommandPalette } from "@/shell/command-palette";
import { ContextSwitcher } from "@/shell/context-switcher";
import { ConsoleHeader } from "@/shell/header";
import { NavigationPanel } from "@/shell/navigation-panel";
import { cn } from "@/lib/utils";
import type { ConsoleKind } from "@/shell/navigation";

function ConsoleSkeleton() {
  return (
    <div className="grid h-dvh min-h-[100svh] grid-cols-1 overflow-hidden bg-background lg:grid-cols-[272px_minmax(0,1fr)]">
      <div className="hidden border-r border-border bg-sidebar-console-bg p-4 lg:block">
        <div className="h-8 w-36 animate-pulse rounded-[4px] bg-slate-800" />
        <div className="mt-12 space-y-2">
          {Array.from({ length: 6 }, (_, index) => (
            <div key={index} className="h-8 animate-pulse rounded-[4px] bg-slate-800/60" />
          ))}
        </div>
      </div>
      <div className="min-w-0">
        <div className="h-14 animate-pulse border-b border-border bg-card" />
        <div className="space-y-5 p-4 sm:p-6">
          <div className="h-8 w-56 animate-pulse rounded-[4px] bg-muted" />
          <div className="h-64 animate-pulse rounded-[6px] border border-border bg-card" />
        </div>
      </div>
    </div>
  );
}

function SessionUnavailable({ message, retry }: { message: string; retry: () => void }) {
  return (
    <div className="flex min-h-[100svh] items-center justify-center bg-background px-6 text-center">
      <div className="max-w-md space-y-4">
        <AlertTriangle className="mx-auto h-8 w-8 text-amber-500" />
        <div>
          <h1 className="text-base font-semibold">Session verification unavailable</h1>
          <p className="mt-1 text-sm text-muted-foreground">{message}</p>
        </div>
        <Button onClick={retry} variant="outline">
          Retry verification
        </Button>
      </div>
    </div>
  );
}

export function ConsoleShell({ kind, children }: { kind: ConsoleKind; children: ReactNode }) {
  const router = useRouter();
  const { status, error, refreshSession, renderContext } = useUserSession();
  const [collapsed, setCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);

  useEffect(() => {
    if (status !== "authenticated" || !renderContext || renderContext.kind === kind) return;
    // The URL root is a renderer boundary, while the Render Context is the
    // verified authority. Never mount the wrong tree during a context race.
    router.replace(renderContext.kind === "personal" ? "/personal" : "/tenant");
  }, [kind, renderContext, router, status]);

  if (status === "verifying" || status === "unauthenticated") return <ConsoleSkeleton />;
  if (status === "error") {
    return <SessionUnavailable message={error} retry={() => void refreshSession()} />;
  }
  if (!renderContext || renderContext.kind !== kind) return <ConsoleSkeleton />;

  return (
    <div
      className={cn(
        "grid h-dvh min-h-[100svh] grid-cols-1 overflow-hidden bg-background text-foreground transition-[grid-template-columns] duration-200",
        collapsed ? "lg:grid-cols-[60px_minmax(0,1fr)]" : "lg:grid-cols-[272px_minmax(0,1fr)]",
      )}
    >
      <aside className="hidden min-h-0 border-r border-sidebar-console-border lg:block">
        <NavigationPanel kind={kind} collapsed={collapsed} onCollapseChange={setCollapsed} />
      </aside>

      <div className="flex min-h-0 min-w-0 flex-col">
        <ConsoleHeader
          kind={kind}
          onOpenMobileNavigation={() => setMobileOpen(true)}
          onToggleDesktopNavigation={() => setCollapsed((current) => !current)}
          onOpenCommandPalette={() => setCommandOpen(true)}
        />
        <main className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 py-4 pb-[max(1rem,env(safe-area-inset-bottom))] sm:px-6 sm:py-5 lg:py-6">
          {children}
        </main>
      </div>

      <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
        <SheetContent
          side="left"
          className="w-[min(88vw,320px)] gap-0 border-sidebar-console-border bg-sidebar-console-bg p-0 text-slate-200"
          showCloseButton
        >
          <SheetHeader className="sr-only">
            <SheetTitle>Console navigation</SheetTitle>
            <SheetDescription>Select a destination, Zone or workspace.</SheetDescription>
          </SheetHeader>
          <div className="pt-[env(safe-area-inset-top)]">
            <ContextSwitcher mobile />
          </div>
          <div className="min-h-0 flex-1">
            <NavigationPanel kind={kind} onNavigate={() => setMobileOpen(false)} />
          </div>
        </SheetContent>
      </Sheet>

      <ConsoleCommandPalette kind={kind} open={commandOpen} onOpenChange={setCommandOpen} />
    </div>
  );
}
