"use client";

import React, { createContext, useContext, useState, useEffect, useCallback } from "react";
import { usePathname } from "next/navigation";
import SidebarConsole from "@/components/sidebar-console";
import HeaderConsole from "@/components/header-console";
import CommandPalette from "@/components/command-palette";
import { useUserSession } from "@/hooks/useUserSession";
import { useTheme, type ThemeMode } from "@/context/ThemeContext";

// [COMMENT]: Giao diện Skeleton Loading cao cấp khi chờ tải phiên làm việc
function ConsoleSkeleton() {
  return (
    <div className="flex min-h-screen bg-background text-foreground">
      {/* [COMMENT]: Sidebar Skeleton đồng bộ chiều cao h-8, bo góc 4px và màu nền bg-sidebar */}
      <div className="w-[272px] border-r border-border p-6 space-y-6 hidden md:block bg-sidebar">
        <div className="flex items-center gap-3 animate-pulse">
          <div className="h-8 w-8 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
          <div className="space-y-2">
            <div className="h-4 w-24 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
            <div className="h-3 w-16 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
          </div>
        </div>
        <div className="space-y-4 pt-8 animate-pulse">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="flex items-center gap-3 h-8 px-3 rounded-[4px] bg-slate-100/50 dark:bg-slate-900/30">
              <div className="h-4 w-4 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
              <div className="h-4 w-28 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
            </div>
          ))}
        </div>
      </div>

      {/* Main Panel Skeleton */}
      <div className="flex-1 flex flex-col min-h-screen">
        {/* [COMMENT]: Header Skeleton đồng bộ màu nền bg-card của Surface */}
        <div className="h-14 border-b border-border px-6 flex items-center justify-between bg-card animate-pulse">
          <div className="flex items-center gap-4">
            <div className="h-4 w-24 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
            <div className="h-4 w-32 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
          </div>
          <div className="flex items-center gap-4">
            <div className="h-8 w-8 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
            <div className="h-8 w-8 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
            <div className="h-8 w-16 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
          </div>
        </div>

        {/* [COMMENT]: Sử dụng cấu trúc container co giãn w-full và px-6 đồng bộ */}
        <main className="flex-1 px-6 py-5 md:py-6 w-full space-y-6 animate-pulse">
          {/* [COMMENT]: Card Skeleton đồng bộ bo góc rounded-[4px] và nền bg-card */}
          <div className="rounded-[4px] border border-border bg-card p-6 space-y-4">
            <div className="h-6 w-48 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
            <div className="h-4 w-96 rounded-[4px] bg-slate-200 dark:bg-slate-800" />
          </div>
        </main>
      </div>
    </div>
  );
}

// [COMMENT]: Định nghĩa kiểu dữ liệu Context chia sẻ giữa các trang con
interface ConsoleLayoutContextType {
  activeId: string;
  setActiveId: (id: string) => void;
  theme: ThemeMode;
  setTheme: (theme: ThemeMode) => void;
}

const ConsoleLayoutContext = createContext<ConsoleLayoutContextType | undefined>(undefined);

export function useConsoleLayout() {
  const context = useContext(ConsoleLayoutContext);
  if (!context) {
    throw new Error("useConsoleLayout must be used within a ConsoleLayoutProvider");
  }
  return context;
}

export default function ConsoleLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const { loading, authenticated } = useUserSession();

  // [COMMENT]: State chia sẻ giữa Sidebar và Main Panel để điều khiển layout co giãn
  const [isCollapsed, setIsCollapsed] = useState(false);
  const [activeId, setActiveId] = useState("overview");
  const [commandPaletteOpen, setCommandPaletteOpen] = useState(false);
  const { theme, setTheme } = useTheme();

  // [COMMENT]: Tự động nhận diện tab hoạt động trên Sidebar dựa theo đường dẫn hiện tại
  useEffect(() => {
    if (pathname.startsWith("/tenants")) {
      setActiveId("tenants");
    } else if (pathname === "/") {
      setActiveId((prev) => (prev === "tenants" ? "overview" : prev));
    }
  }, [pathname]);

  if (loading || !authenticated) {
    return <ConsoleSkeleton />;
  }

  return (
    <ConsoleLayoutContext.Provider value={{ activeId, setActiveId, theme, setTheme }}>
      {/* [COMMENT]: Sử dụng các màu nền bg-background và text-foreground chuẩn semantic để chuyển đổi mượt mà */}
      <div className="min-h-screen bg-background text-foreground flex overflow-hidden transition-colors duration-150">
        <SidebarConsole
          isCollapsed={isCollapsed}
          setIsCollapsed={setIsCollapsed}
          activeId={activeId}
          setActiveId={setActiveId}
        />

        <div
          className="flex-1 min-w-0 transition-all duration-300 ease-in-out flex flex-col min-h-screen"
          style={{ marginLeft: isCollapsed ? "60px" : "272px" }}
        >
          <HeaderConsole
            isCollapsed={isCollapsed}
            setIsCollapsed={setIsCollapsed}
            activeId={activeId}
            theme={theme}
            setTheme={setTheme}
            onOpenCommandPalette={() => setCommandPaletteOpen(true)}
          />

          {/* [COMMENT]: Thống nhất dùng padding-inline: 24px (px-6) và co giãn chiếm 100% không gian trống còn lại */}
          <main className="flex-1 px-6 py-5 md:py-6 w-full animate-in fade-in duration-200">
            {children}
          </main>
        </div>

        <CommandPalette
          isOpen={commandPaletteOpen}
          setIsOpen={setCommandPaletteOpen}
          setActivePage={() => {}}
        />
      </div>
    </ConsoleLayoutContext.Provider>
  );
}
