"use client";

import React, { useCallback } from "react";
import { Menu, Search, ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";
import { useRouter } from "next/navigation";
import { authAPI } from "@/lib/api/auth";
import { useUserSession } from "@/hooks/useUserSession";
import { useWorkspace } from "@/context/WorkspaceContext";
import { type ThemeMode } from "@/context/ThemeContext";

// Import subcomponents
import { WorkspaceSelector } from "./workspace-selector";
import { ZoneSelector } from "./zone-selector";
import { HealthIndicator } from "./health-indicator";
import { HelpMenu } from "./help-menu";
import { ThemeSwitcher } from "./theme-switcher";
import { UserProfile } from "./user-profile";
import { NotificationsDrawer } from "./notifications-drawer";

interface HeaderConsoleProps {
  isCollapsed: boolean;
  setIsCollapsed: (collapsed: boolean) => void;
  activeId: string;
  theme: ThemeMode;
  setTheme: (theme: ThemeMode) => void;
  onOpenCommandPalette: () => void;
}

export default function HeaderConsole({
  isCollapsed,
  setIsCollapsed,
  activeId,
  theme,
  setTheme,
  onOpenCommandPalette
}: HeaderConsoleProps) {
  const router = useRouter();
  const { clearSession, profile } = useUserSession();
  const { catalog, activeWorkspaceID, selectWorkspace, loading: wsLoading } = useWorkspace();

  // [COMMENT]: Xử lý đăng xuất phiên làm việc của user
  const handleLogout = useCallback(async () => {
    try {
      await authAPI.logout();
    } catch (e) {
      console.error("[Logout] Failed to call logout endpoint", e);
    } finally {
      clearSession();
      router.push("/signin");
    }
  }, [clearSession, router]);

  // [COMMENT]: Bản đồ ánh xạ Breadcrumb chuẩn xác dựa theo Active ID từ Sidebar
  // Loại bỏ các tiền tố trùng lặp (ví dụ Workspaces Workspaces) và chuẩn hóa phân nhóm điều hướng.
  const getBreadcrumb = () => {
    const mapping: Record<string, string[]> = {
      overview: ["Console", "Overview"],
      workspaces: ["Console", "Workspaces"],
      storage: ["Console", "Object Storage"],
      mail: ["Console", "Email Delivery"],
      users: ["Console", "User Directory"],
      rbac: ["Console", "Access Control"],
      role: ["Console", "Access Control"],
      zones: ["Infrastructure", "Zones"],
      hypervisors: ["Infrastructure", "Hypervisors"],
      networks: ["Infrastructure", "Networks"],
      "load-balancers": ["Infrastructure", "Load Balancers"],
      kubernetes: ["Platform", "Kubernetes"],
      resources: ["Platform", "Resources"],
      templates: ["Platform", "Templates"],
      tenants: ["Workspaces", "Tenants"],
      vms: ["Workspaces", "Virtual Machines"],
      vault: ["Security", "Vault"],
      iam: ["Security", "Identity & Access"],
      audit: ["Security", "Audit Logs"],
      monitoring: ["Operations", "Monitoring"],
      incidents: ["Operations", "Incidents"],
      activity: ["Operations", "Activity"],
      settings: ["Administration", "Settings"]
    };

    return mapping[activeId] || ["Console", "Overview"];
  };

  const breadcrumbs = getBreadcrumb();

  return (
    <header className="h-14 shrink-0 border-b bg-white border-slate-200 dark:bg-sidebar-console-bg dark:border-sidebar-console-border flex items-center justify-between px-6 z-40 transition-colors select-none">
      {/* ========================================== */}
      {/* 1. VÙNG TRÁI: Toggle menu, Breadcrumbs, Search */}
      {/* ========================================== */}
      <div className="flex items-center gap-4 min-w-0 flex-1">
        {/* Nút trigger đóng mở Sidebar */}
        <button
          onClick={() => setIsCollapsed(!isCollapsed)}
          className="p-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white cursor-pointer transition-colors shrink-0"
          title="Toggle Navigation"
        >
          <Menu className="h-4 w-4" />
        </button>

        {/* Breadcrumb phản ánh đúng luồng cấu trúc thư mục của Control Plane */}
        <div className="hidden lg:flex items-center gap-1.5 text-xs font-semibold text-slate-500 dark:text-slate-400 shrink-0">
          {breadcrumbs.map((crumb, idx) => (
            <React.Fragment key={crumb}>
              {idx > 0 && <ChevronRight className="h-3 w-3 text-slate-300 dark:text-slate-600" />}
              <span className={cn(
                idx === breadcrumbs.length - 1 ? "text-slate-800 dark:text-slate-200 font-bold" : "font-medium"
              )}>
                {crumb}
              </span>
            </React.Fragment>
          ))}
        </div>

        {/* Global Search Bar */}
        <div
          onClick={onOpenCommandPalette}
          className="relative max-w-[380px] w-full hidden md:block cursor-pointer"
        >
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-slate-400" />
          <div className="w-full bg-slate-50 border border-slate-200 hover:border-slate-300 dark:bg-slate-900/60 dark:border-slate-800 dark:hover:border-slate-700/80 rounded-lg pl-9 pr-12 py-1.5 text-xs text-slate-400 flex justify-between items-center transition-all select-none">
            <span>Search resources, VMs, tenants...</span>
            <span className="text-[9px] font-mono bg-white border border-slate-200 dark:bg-slate-800 dark:border-slate-700 text-slate-500 px-1 rounded-sm shadow-xs">
              ⌘K
            </span>
          </div>
        </div>
      </div>

      {/* ========================================== */}
      {/* 2. VÙNG GIỮA: Context Selectors (Workspace, Region) */}
      {/* ========================================== */}
      <div className="hidden md:flex items-center gap-3 shrink-0 px-4">
        <WorkspaceSelector
          catalog={catalog}
          activeWorkspaceID={activeWorkspaceID}
          selectWorkspace={selectWorkspace}
          loading={wsLoading}
        />
        <div className="h-4 w-px bg-slate-200 dark:bg-slate-800" />
        <ZoneSelector />
      </div>

      {/* ========================================== */}
      {/* 3. VÙNG PHẢI: Actions, Health, Help, Theme, User, Notifications */}
      {/* ========================================== */}
      <div className="flex items-center gap-2 shrink-0">
        <HealthIndicator />
        <HelpMenu />
        <ThemeSwitcher theme={theme} setTheme={setTheme} />
        
        <div className="h-4 w-px bg-slate-200 dark:bg-slate-800" />
        
        <UserProfile profile={profile} handleLogout={handleLogout} />
        
        <div className="h-4 w-px bg-slate-200 dark:bg-slate-800" />
        
        <NotificationsDrawer />
      </div>
    </header>
  );
}
