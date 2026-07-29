"use client";

import { useCallback } from "react";
import { ChevronRight, Menu, PanelLeft, Search } from "lucide-react";
import { usePathname, useRouter } from "next/navigation";

import { NotificationsDrawer } from "@/features/notifications/drawer";
import { WalletBalance } from "@/features/billing/wallet-balance";
import { ThemeSwitcher } from "@/components/header/theme-switcher";
import { UserProfile } from "@/shell/user-profile";
import { authAPI } from "@/features/auth/api";
import { useTheme } from "@/context/ThemeContext";
import { useUserSession } from "@/session/use-session";
import { ContextSwitcher } from "@/shell/context-switcher";
import { activeNavigation, consoleNavigation } from "@/shell/navigation";

type ConsoleHeaderProps = {
  onOpenMobileNavigation: () => void;
  onToggleDesktopNavigation: () => void;
  onOpenCommandPalette: () => void;
};

export function ConsoleHeader({
  onOpenMobileNavigation,
  onToggleDesktopNavigation,
  onOpenCommandPalette,
}: ConsoleHeaderProps) {
  const router = useRouter();
  const pathname = usePathname();
  const { clearSession, profile, renderContext, checkPermission } = useUserSession();
  const { theme, setTheme } = useTheme();
  const items = consoleNavigation(renderContext?.is_personal ?? true, checkPermission);
  const active = activeNavigation(pathname, items);
  const breadcrumb = pathname.startsWith("/settings") ? ["Console", "Settings"] : active.breadcrumb;

  const handleLogout = useCallback(async () => {
    try {
      await authAPI.logout();
    } catch {
      // Local teardown is mandatory even when a failed replica cannot acknowledge logout.
    } finally {
      clearSession("logout");
      router.replace("/signin");
    }
  }, [clearSession, router]);

  return (
    <header className="z-40 flex h-14 shrink-0 items-center justify-between gap-2 border-b border-border bg-card px-3 sm:px-4 lg:px-6">
      <div className="flex min-w-0 flex-1 items-center gap-3">
        <button
          type="button"
          onClick={onOpenMobileNavigation}
          className="rounded-[4px] p-1.5 text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-blue-500 lg:hidden"
          aria-label="Open navigation"
        >
          <Menu className="h-4 w-4" />
        </button>
        <button
          type="button"
          onClick={onToggleDesktopNavigation}
          className="hidden rounded-[4px] p-1.5 text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-blue-500 lg:block"
          aria-label="Toggle navigation width"
        >
          <PanelLeft className="h-4 w-4" />
        </button>

        <div className="hidden min-w-0 items-center gap-1.5 text-xs md:flex" aria-label="Breadcrumb">
          {breadcrumb.map((crumb, index) => (
            <span key={crumb} className="contents">
              {index > 0 && <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground" />}
              <span className={index === breadcrumb.length - 1 ? "truncate font-semibold text-foreground" : "text-muted-foreground"}>
                {crumb}
              </span>
            </span>
          ))}
        </div>

        <button
          type="button"
          onClick={onOpenCommandPalette}
          className="hidden h-8 w-full max-w-[360px] items-center gap-2 rounded-[6px] border border-border bg-muted/40 px-3 text-xs text-muted-foreground outline-none hover:border-slate-300 focus-visible:ring-2 focus-visible:ring-blue-500 xl:flex"
        >
          <Search className="h-3.5 w-3.5" />
          <span className="truncate">Search Console destinations…</span>
          <kbd className="ml-auto font-mono text-[10px]">⌘K</kbd>
        </button>
      </div>

      <div className="hidden shrink-0 2xl:block">
        <ContextSwitcher />
      </div>

      <div className="flex shrink-0 items-center gap-1 sm:gap-2">
        <WalletBalance />
        <ThemeSwitcher theme={theme} setTheme={setTheme} />
        <UserProfile profile={profile} handleLogout={handleLogout} />
        <NotificationsDrawer />
      </div>
    </header>
  );
}
