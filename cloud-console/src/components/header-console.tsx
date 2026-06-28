"use client";

import React, { useState, useEffect, useRef, useCallback } from "react";
import {
  Menu,
  Search,
  ChevronDown,
  Bell,
  HelpCircle,
  Sun,
  Moon,
  ShieldCheck,
  ChevronRight,
  LogOut,
  User,
  Key,
  Sliders,
  Sparkles
} from "lucide-react";
import { cn } from "@/lib/utils";
import { fetchZoneCatalog, switchZone, type ZoneCatalogItem } from "@/lib/api/zone";
import { toast } from "sonner";
import { useRouter } from "next/navigation";
import { authAPI } from "@/lib/api/auth";
import { useUserSession } from "@/hooks/useUserSession";

// [COMMENT]: Định nghĩa Interfaces cho các sự kiện dữ liệu Header
interface HeaderConsoleProps {
  isCollapsed: boolean;
  setIsCollapsed: (collapsed: boolean) => void;
  activeId: string;
  theme: "light" | "dark";
  setTheme: (theme: "light" | "dark") => void;
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
  const { clearSession } = useUserSession();

  // [COMMENT]: Xử lý đăng xuất phiên làm việc của user
  const handleLogout = useCallback(async () => {
    setUserOpen(false);
    try {
      await authAPI.logout();
    } catch (e) {
      console.error("[Logout] Failed to call logout endpoint", e);
    } finally {
      clearSession();
      router.push("/signin");
    }
  }, [clearSession, router]);

  // [COMMENT]: Quản lý state đóng/mở cho các menu Dropdown trong Header
  const [workspaceOpen, setWorkspaceOpen] = useState(false);
  const [zoneOpen, setZoneOpen] = useState(false);
  const [healthOpen, setHealthOpen] = useState(false);
  const [notifOpen, setNotifOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [userOpen, setUserOpen] = useState(false);

  // [COMMENT]: State quản lý context làm việc được chọn hiện tại
  const [activeWorkspace, setActiveWorkspace] = useState("Production");
  const [activeZone, setActiveZone] = useState("");
  const [zones, setZones] = useState<ZoneCatalogItem[]>([]);

  // [COMMENT]: References để hỗ trợ đóng dropdown khi người dùng click ra ngoài vùng hiển thị
  const workspaceRef = useRef<HTMLDivElement>(null);
  const zoneRef = useRef<HTMLDivElement>(null);
  const healthRef = useRef<HTMLDivElement>(null);
  const notifRef = useRef<HTMLDivElement>(null);
  const helpRef = useRef<HTMLDivElement>(null);
  const userRef = useRef<HTMLDivElement>(null);

  // [COMMENT]: Mock dữ liệu cho trạng thái và thông báo hệ thống
  const microserviceHealth = [
    { name: "Control API", status: "Healthy" },
    { name: "Block Storage Service", status: "Healthy" },
    { name: "SDN Network Router", status: "Healthy" },
    { name: "Hypervisor Controller", status: "Healthy" }
  ];

  const mockNotifications = [
    { title: "Hypervisor node-04 disconnected", type: "error", time: "5 mins ago" },
    { title: "Storage pool ceph-hdd-01 resized (+20TB)", type: "info", time: "1 hour ago" },
    { title: "New tenant workspace 'Acme Labs' created", type: "success", time: "3 hours ago" }
  ];

  // [COMMENT]: Bản đồ ánh xạ Breadcrumb dựa theo Active ID từ Sidebar
  const getBreadcrumb = () => {
    const mapping: Record<string, string[]> = {
      overview: ["Overview"],
      zones: ["Infrastructure", "Zones"],
      hypervisors: ["Infrastructure", "Hypervisors"],
      storage: ["Infrastructure", "Storage"],
      networks: ["Infrastructure", "Networks"],
      "load-balancers": ["Infrastructure", "Load Balancers"],
      kubernetes: ["Platform", "Kubernetes"],
      resources: ["Platform", "Resources"],
      templates: ["Platform", "Templates"],
      tenants: ["Workspaces", "Tenants"],
      workspaces: ["Workspaces", "Workspaces"],
      vms: ["Workspaces", "Virtual Machines"],
      vault: ["Security", "Vault"],
      iam: ["Security", "Identity & Access"],
      audit: ["Security", "Audit Logs"],
      monitoring: ["Operations", "Monitoring"],
      incidents: ["Operations", "Incidents"],
      activity: ["Operations", "Activity"],
      users: ["Administration", "Users"],
      settings: ["Administration", "Settings"]
    };

    return mapping[activeId] || ["Overview"];
  };

  const breadcrumbs = getBreadcrumb();

  // [COMMENT]: Tự động lắng nghe click bên ngoài cho tất cả dropdown để tối ưu bộ nhớ và trải nghiệm
  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      const target = e.target as Node;
      if (workspaceRef.current && !workspaceRef.current.contains(target)) setWorkspaceOpen(false);
      if (zoneRef.current && !zoneRef.current.contains(target)) setZoneOpen(false);
      if (healthRef.current && !healthRef.current.contains(target)) setHealthOpen(false);
      if (notifRef.current && !notifRef.current.contains(target)) setNotifOpen(false);
      if (helpRef.current && !helpRef.current.contains(target)) setHelpOpen(false);
      if (userRef.current && !userRef.current.contains(target)) setUserOpen(false);
    };

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  // [COMMENT]: Gọi API từ ACR/Controlplane để lấy danh sách Active Zones của User
  useEffect(() => {
    let active = true;
    fetchZoneCatalog()
      .then((data) => {
        if (active && data) {
          setZones(data);
          if (data.length > 0) {
            // Đặt Zone đầu tiên là Active Zone mặc định
            setActiveZone(data[0].code.toUpperCase());
          }
        }
      })
      .catch((err) => {
        console.error("[Header] Failed to fetch active zones list:", err);
      });
    return () => {
      active = false;
    };
  }, []);

  return (
    <header className="h-14 shrink-0 border-b bg-white border-slate-200 dark:bg-sidebar-console-bg dark:border-sidebar-console-border flex items-center justify-between px-6 z-40 transition-colors select-none">

      {/* ========================================== */}
      {/* 1. VÙNG TRÁI: Toggle menu, Breadcrumbs, Search */}
      {/* ========================================== */}
      <div className="flex items-center gap-4 min-w-0 flex-1">
        {/* [COMMENT]: Nút trigger đóng mở Sidebar */}
        <button
          onClick={() => setIsCollapsed(!isCollapsed)}
          className="p-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white cursor-pointer transition-colors shrink-0"
          title="Toggle Navigation"
        >
          <Menu className="h-4 w-4" />
        </button>

        {/* [COMMENT]: Breadcrumb phản ánh đúng luồng cấu trúc thư mục của Control Plane */}
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

        {/* [COMMENT]: Global Search Bar (width: 320px~420px) */}
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
        {/* [COMMENT]: Dropdown chọn Workspace context */}
        <div ref={workspaceRef} className="relative">
          <button
            onClick={() => setWorkspaceOpen(!workspaceOpen)}
            className="flex items-center gap-1.5 text-xs font-semibold px-2.5 py-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-700 dark:text-slate-300 cursor-pointer transition-colors"
          >
            <span className="text-slate-400 dark:text-slate-500 font-normal">Workspace:</span>
            <span>{activeWorkspace}</span>
            <ChevronDown className="h-3 w-3 text-slate-400" />
          </button>

          {workspaceOpen && (
            <div className="absolute top-[110%] left-0 w-44 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-xl py-1 z-50 animate-in fade-in slide-in-from-top-1 duration-100">
              {["Production", "Staging", "Development"].map((ws) => (
                <button
                  key={ws}
                  onClick={() => {
                    setActiveWorkspace(ws);
                    setWorkspaceOpen(false);
                  }}
                  className={cn(
                    "w-full text-left px-3 py-1.5 text-xs font-semibold hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer text-slate-700 dark:text-slate-300",
                    ws === activeWorkspace && "text-blue-500 dark:text-blue-400 bg-slate-50 dark:bg-slate-800/40"
                  )}
                >
                  {ws}
                </button>
              ))}
            </div>
          )}
        </div>

        <div className="h-4 w-px bg-slate-200 dark:bg-slate-800" />

        {/* [COMMENT]: Dropdown chọn Active Zone context kết nối cổng API Gateway của ACR */}
        <div ref={zoneRef} className="relative">
          <button
            onClick={() => setZoneOpen(!zoneOpen)}
            className="flex items-center gap-1.5 text-xs font-semibold px-2.5 py-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-700 dark:text-slate-300 cursor-pointer transition-colors"
          >
            <span className="text-slate-400 dark:text-slate-500 font-normal">Zone:</span>
            <span>{activeZone || "Loading..."}</span>
            <ChevronDown className="h-3 w-3 text-slate-400" />
          </button>

          {zoneOpen && zones.length > 0 && (
            <div className="absolute top-[110%] left-0 w-48 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-xl py-1 z-50 animate-in fade-in slide-in-from-top-1 duration-100">
              {zones.map((z) => (
                <button
                  key={z.code}
                  onClick={async () => {
                    try {
                      await switchZone(z.code);
                      setActiveZone(z.code.toUpperCase());
                      setZoneOpen(false);
                      toast.success(`Switched to zone: ${z.name}`);
                      // [COMMENT]: Làm mới trang để đồng bộ và cập nhật lại toàn bộ tài nguyên VM/Incident của Zone mới
                      window.location.reload();
                    } catch (err: any) {
                      toast.error(err.message || "Failed to switch active zone");
                    }
                  }}
                  className={cn(
                    "w-full text-left px-3 py-2 text-xs font-semibold hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer text-slate-700 dark:text-slate-300",
                    z.code.toUpperCase() === activeZone && "text-blue-500 dark:text-blue-400 bg-slate-50 dark:bg-slate-800/40"
                  )}
                >
                  <div className="font-bold">{z.name}</div>
                  <div className="text-[10px] text-slate-400 font-mono">{z.code}</div>
                </button>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* ========================================== */}
      {/* 3. VÙNG PHẢI: Actions, Health, Help, Theme, User */}
      {/* ========================================== */}
      <div className="flex items-center gap-2 shrink-0">

        {/* [COMMENT]: Platform Service Health Indicator */}
        <div ref={healthRef} className="relative">
          <button
            onClick={() => setHealthOpen(!healthOpen)}
            className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-xs font-semibold text-emerald-600 dark:text-emerald-400 cursor-pointer transition-colors"
          >
            <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
            <span className="hidden sm:inline">Healthy</span>
          </button>

          {healthOpen && (
            <div className="absolute top-[110%] right-0 w-60 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-xl p-3.5 z-50 animate-in fade-in slide-in-from-top-1 duration-100">
              <div className="flex items-center gap-1.5 text-xs font-bold text-slate-800 dark:text-slate-200 mb-2 pb-1.5 border-b border-slate-100 dark:border-slate-800">
                <ShieldCheck className="h-4 w-4 text-emerald-500" />
                <span>All Core Services Operational</span>
              </div>
              <div className="space-y-2">
                {microserviceHealth.map((svc) => (
                  <div key={svc.name} className="flex justify-between items-center text-[11px]">
                    <span className="text-slate-600 dark:text-slate-400">{svc.name}</span>
                    <span className="font-semibold text-emerald-500">{svc.status}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* [COMMENT]: Notifications Panel */}
        <div ref={notifRef} className="relative">
          <button
            onClick={() => setNotifOpen(!notifOpen)}
            className="p-2 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white cursor-pointer transition-colors relative"
          >
            <Bell className="h-4 w-4" />
            <span className="absolute top-1 right-1 flex h-2 w-2">
              <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-blue-400 opacity-75" />
              <span className="relative inline-flex rounded-full h-2 w-2 bg-blue-500" />
            </span>
          </button>

          {notifOpen && (
            <div className="absolute top-[110%] right-0 w-80 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-xl p-3 z-50 animate-in fade-in slide-in-from-top-1 duration-100">
              <h3 className="text-xs font-bold text-slate-800 dark:text-slate-200 pb-2 border-b border-slate-100 dark:border-slate-800 mb-2">
                Platform Notifications
              </h3>
              <div className="space-y-3">
                {mockNotifications.map((notif, idx) => (
                  <div key={idx} className="space-y-0.5 text-left">
                    <p className="text-xs font-semibold text-slate-700 dark:text-slate-300 leading-tight">
                      {notif.title}
                    </p>
                    <div className="flex justify-between text-[10px] text-slate-400 font-mono">
                      <span>System Event</span>
                      <span>{notif.time}</span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* [COMMENT]: Help Menu */}
        <div ref={helpRef} className="relative">
          <button
            onClick={() => setHelpOpen(!helpOpen)}
            className="p-2 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white cursor-pointer transition-colors"
          >
            <HelpCircle className="h-4 w-4" />
          </button>

          {helpOpen && (
            <div className="absolute top-[110%] right-0 w-48 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-xl py-1.5 z-50 animate-in fade-in slide-in-from-top-1 duration-100">
              {["Documentation", "API Reference", "Release Notes", "Technical Support"].map((item) => (
                <button
                  key={item}
                  className="w-full text-left px-3 py-1.5 text-xs font-semibold hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer text-slate-700 dark:text-slate-300"
                  onClick={() => setHelpOpen(false)}
                >
                  {item}
                </button>
              ))}
            </div>
          )}
        </div>

        {/* [COMMENT]: Theme Switcher */}
        <button
          onClick={() => setTheme(theme === "light" ? "dark" : "light")}
          className="p-2 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white cursor-pointer transition-colors"
          title={theme === "light" ? "Switch to Dark Mode" : "Switch to Light Mode"}
        >
          {theme === "light" ? <Moon className="h-4 w-4" /> : <Sun className="h-4 w-4" />}
        </button>

        <div className="h-4 w-px bg-slate-200 dark:bg-slate-800" />

        {/* [COMMENT]: User Profile Dropdown (double lines: System Admin, Platform Administrator) */}
        <div ref={userRef} className="relative">
          <button
            onClick={() => setUserOpen(!userOpen)}
            className="flex items-center gap-2.5 px-2 py-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover cursor-pointer transition-colors text-left"
          >
            <div className="h-7 w-7 rounded-full bg-blue-600 shrink-0 text-white flex items-center justify-center font-bold text-xs shadow-inner">
              SA
            </div>
            <div className="hidden xl:flex flex-col select-none">
              <span className="text-xs font-bold text-slate-800 dark:text-slate-200 leading-tight">
                System Admin
              </span>
              <span className="text-[9px] text-slate-400 dark:text-slate-500 font-semibold leading-none">
                Platform Administrator
              </span>
            </div>
            <ChevronDown className="h-3 w-3 text-slate-400 hidden sm:block shrink-0" />
          </button>

          {userOpen && (
            <div className="absolute top-[110%] right-0 w-52 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-xl py-1.5 z-50 animate-in fade-in slide-in-from-top-1 duration-100">
              <div className="px-3 py-2 border-b border-slate-100 dark:border-slate-800 mb-1 xl:hidden">
                <p className="text-xs font-bold text-slate-800 dark:text-slate-200">System Admin</p>
                <p className="text-[10px] text-slate-400 dark:text-slate-500">Platform Administrator</p>
              </div>

              <button
                className="w-full flex items-center gap-2 px-3 py-2 text-xs font-semibold text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer text-left"
                onClick={() => setUserOpen(false)}
              >
                <User className="h-4.5 w-4.5 text-slate-400" />
                <span>My Profile</span>
              </button>
              <button
                className="w-full flex items-center gap-2 px-3 py-2 text-xs font-semibold text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer text-left"
                onClick={() => setUserOpen(false)}
              >
                <Key className="h-4.5 w-4.5 text-slate-400" />
                <span>API Keys</span>
              </button>
              <button
                className="w-full flex items-center gap-2 px-3 py-2 text-xs font-semibold text-slate-700 dark:text-slate-300 hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer text-left"
                onClick={() => setUserOpen(false)}
              >
                <Sliders className="h-4.5 w-4.5 text-slate-400" />
                <span>Preferences</span>
              </button>

              <div className="h-px bg-slate-100 dark:bg-slate-800 my-1" />

              <button
                className="w-full flex items-center gap-2 px-3 py-2 text-xs font-semibold text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/20 cursor-pointer text-left"
                onClick={handleLogout}
              >
                <LogOut className="h-4.5 w-4.5" />
                <span>Log Out</span>
              </button>
            </div>
          )}
        </div>

      </div>
    </header>
  );
}
