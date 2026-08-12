"use client";

import { usePathname, useRouter } from "next/navigation";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { toast } from "sonner";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { useUserSession } from "@/session/use-session";
import {
  activeNavigation,
  billingStartURL,
  personalConsoleNavigation,
  tenantConsoleNavigation,
  type ConsoleKind,
  type NavigationItem,
} from "@/shell/navigation";

type NavigationPanelProps = {
	kind: ConsoleKind;
  collapsed?: boolean;
  onCollapseChange?: (collapsed: boolean) => void;
  onNavigate?: () => void;
};

function NavigationButton({
  item,
  active,
  collapsed,
  onSelect,
}: {
  item: NavigationItem;
  active: boolean;
  collapsed: boolean;
  onSelect: () => void;
}) {
  const Icon = item.icon;
  const button = (
    <button
      type="button"
      aria-current={active ? "page" : undefined}
      aria-label={collapsed ? item.label : undefined}
      onClick={onSelect}
      className={cn(
        "relative flex h-8 w-full items-center gap-2.5 rounded-[4px] px-2.5 text-left text-[13px] font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-blue-400",
        active
          ? "bg-sidebar-console-active-bg font-semibold text-blue-400 before:absolute before:inset-y-[2px] before:left-0 before:w-[3px] before:rounded-r before:bg-sidebar-console-active-border"
          : "text-slate-400 hover:bg-sidebar-console-hover hover:text-slate-200",
      )}
    >
      <Icon className={cn("h-3.5 w-3.5 shrink-0", active ? "text-blue-400" : "text-slate-500")} />
      {!collapsed && <span className="truncate">{item.label}</span>}
    </button>
  );

  if (!collapsed) return button;
  return (
    <Tooltip>
      <TooltipTrigger render={button} />
      <TooltipContent side="right">{item.label}</TooltipContent>
    </Tooltip>
  );
}

export function NavigationPanel({
  kind,
  collapsed = false,
  onCollapseChange,
  onNavigate,
}: NavigationPanelProps) {
  const router = useRouter();
  const pathname = usePathname();
  const { checkPermission, profile } = useUserSession();
  const items = kind === "personal"
    ? personalConsoleNavigation(checkPermission)
    : tenantConsoleNavigation(checkPermission);
  const consoleItems = items.filter((item) => item.external !== "billing" && item.id !== "context");
  const billingItem = items.find((item) => item.external === "billing");
  const contextItem = items.find((item) => item.id === "context");
  const active = activeNavigation(pathname, items.filter((item) => item.external !== "billing"));

  const navigate = (item: NavigationItem) => {
    if (item.external === "billing") {
      const target = billingStartURL();
      if (!target) {
        toast.error("Cost Console is not configured for this deployment.");
        return;
      }
      window.open(target, "_blank", "noopener,noreferrer");
      onNavigate?.();
      return;
    }
    if (item.path && item.path !== pathname) router.push(item.path);
    onNavigate?.();
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-sidebar-console-bg text-slate-200">
      <div className="flex h-16 shrink-0 items-center border-b border-sidebar-console-border px-3">
        <div className="flex min-w-0 items-center gap-3 overflow-hidden">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-[4px] bg-blue-600 text-lg font-bold text-white">
            ☁
          </div>
          {!collapsed && (
            <div className="min-w-0 leading-tight">
              <div className="truncate text-[15px] font-semibold text-slate-100">Aurora Cloud</div>
              <div className="text-[10px] font-medium uppercase tracking-wider text-blue-400/90">
                Control Plane
              </div>
            </div>
          )}
        </div>
      </div>

      <nav className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-3 py-4" aria-label="Console navigation">
        {!collapsed && (
          <div className="mb-1 px-3 text-[10px] font-bold uppercase tracking-wider text-slate-500">
            Console
          </div>
        )}
        <div className="space-y-[2px]">
          {consoleItems.map((item) => (
            <NavigationButton
              key={item.id}
              item={item}
              active={active.id === item.id}
              collapsed={collapsed}
              onSelect={() => navigate(item)}
            />
          ))}
        </div>
      </nav>

      {(billingItem || contextItem) && (
        <div className="shrink-0 border-t border-sidebar-console-border px-3 py-3">
          {!collapsed && (
            <div className="mb-1 px-3 text-[10px] font-bold uppercase tracking-wider text-slate-500">
              Platform
            </div>
          )}
          {contextItem && <NavigationButton item={contextItem} active={active.id === contextItem.id} collapsed={collapsed} onSelect={() => navigate(contextItem)} />}
          {billingItem && <NavigationButton item={billingItem} active={false} collapsed={collapsed} onSelect={() => navigate(billingItem)} />}
        </div>
      )}

      <div className="shrink-0 border-t border-sidebar-console-border bg-[#0B0F19]/50 p-3">
        {!collapsed && (
          <div className="truncate text-[11px] text-slate-400">
            Signed in as <span className="font-semibold text-slate-200">{profile?.fullname}</span>
          </div>
        )}
        {onCollapseChange && (
          <button
            type="button"
            onClick={() => onCollapseChange(!collapsed)}
            className="mt-2 flex h-8 w-full items-center justify-center rounded-[4px] text-slate-400 outline-none hover:bg-sidebar-console-hover hover:text-white focus-visible:ring-2 focus-visible:ring-blue-400"
            aria-label={collapsed ? "Expand navigation" : "Collapse navigation"}
          >
            {collapsed ? <ChevronRight className="h-4 w-4" /> : <ChevronLeft className="h-4 w-4" />}
          </button>
        )}
      </div>
    </div>
  );
}
