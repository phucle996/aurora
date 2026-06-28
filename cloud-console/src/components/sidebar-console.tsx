"use client";

import React, { useState } from "react";
import {
  Compass,
  Globe,
  Cpu,
  HardDrive,
  Network,
  GitMerge,
  Layers,
  FolderGit,
  Copy,
  Building2,
  LayoutGrid,
  Server,
  Lock,
  UserCheck,
  History,
  Activity,
  AlertCircle,
  Clock,
  Users,
  Settings,
  ChevronLeft,
  ChevronRight,
  ShieldAlert,
  CircleDot
} from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

// [COMMENT]: Định nghĩa cấu trúc Item trong Sidebar. 
// Đảm bảo kiểu dữ liệu chặt chẽ cho từng Domain và Menu Item.
interface SidebarItem {
  id: string;
  name: string;
  icon: React.ComponentType<{ className?: string }>;
  badge?: number;
}

interface SidebarGroup {
  title: string;
  items: SidebarItem[];
}

interface SidebarConsoleProps {
  isCollapsed: boolean;
  setIsCollapsed: (collapsed: boolean) => void;
  activeId: string;
  setActiveId: (id: string) => void;
}

export default function SidebarConsole({
  isCollapsed,
  setIsCollapsed,
  activeId,
  setActiveId
}: SidebarConsoleProps) {
  
  // [COMMENT]: Danh sách các nhóm và item menu được phân chia chuẩn hóa theo triết lý "domain-driven boundary"
  // Thay vì đặt Dashboard lên đầu, nhóm OVERVIEW và INFRASTRUCTURE được đưa lên ưu tiên.
  const menuGroups: SidebarGroup[] = [
    {
      title: "Overview",
      items: [
        { id: "overview", name: "Overview", icon: Compass }
      ]
    },
    {
      title: "Infrastructure",
      items: [
        { id: "zones", name: "Zones", icon: Globe },
        { id: "hypervisors", name: "Hypervisors", icon: Cpu },
        { id: "storage", name: "Storage", icon: HardDrive },
        { id: "networks", name: "Networks", icon: Network },
        { id: "load-balancers", name: "Load Balancers", icon: GitMerge }
      ]
    },
    {
      title: "Platform",
      items: [
        { id: "kubernetes", name: "Kubernetes", icon: Layers },
        { id: "resources", name: "Resources", icon: FolderGit },
        { id: "templates", name: "Templates", icon: Copy }
      ]
    },
    {
      title: "Workspaces",
      items: [
        { id: "tenants", name: "Tenants", icon: Building2 },
        { id: "workspaces", name: "Workspaces", icon: LayoutGrid },
        { id: "vms", name: "Virtual Machines", icon: Server }
      ]
    },
    {
      title: "Security",
      items: [
        { id: "vault", name: "Vault", icon: Lock },
        { id: "iam", name: "Identity & Access", icon: UserCheck },
        { id: "audit", name: "Audit Logs", icon: History }
      ]
    },
    {
      title: "Operations",
      items: [
        { id: "monitoring", name: "Monitoring", icon: Activity },
        { id: "incidents", name: "Incidents", icon: AlertCircle, badge: 2 },
        { id: "activity", name: "Activity", icon: Clock }
      ]
    },
    {
      title: "Administration",
      items: [
        { id: "users", name: "Users", icon: Users },
        { id: "settings", name: "Settings", icon: Settings }
      ]
    }
  ];

  return (
    <aside
      className={cn(
        "fixed top-0 bottom-0 left-0 z-50 flex flex-col bg-sidebar-console-bg text-slate-200 border-r border-sidebar-console-border transition-all duration-300 ease-in-out select-none",
        isCollapsed ? "w-[60px]" : "w-[272px]"
      )}
    >
      {/* [COMMENT]: Header Logo area. Chiều cao chuẩn 64px, phân tách rõ ràng */}
      <div className="flex h-16 items-center justify-between px-4 border-b border-sidebar-console-border relative shrink-0">
        <div className="flex items-center gap-3 overflow-hidden">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-blue-600 shrink-0 text-white font-bold text-lg">
            ☁
          </div>
          {!isCollapsed && (
            <div className="flex flex-col tracking-tight animate-in fade-in duration-200">
              <span className="font-semibold text-[15px] leading-tight text-slate-100">
                Aurora Cloud
              </span>
              <span className="text-[10px] font-medium uppercase text-blue-400/90 tracking-wider">
                Control Plane
              </span>
            </div>
          )}
        </div>

        {/* [COMMENT]: Nút toggle collapse sidebar. Vị trí tuyệt đối giúp điều khiển mượt mà */}
        <button
          onClick={() => setIsCollapsed(!isCollapsed)}
          className="absolute -right-3 top-1/2 -translate-y-1/2 flex h-6 w-6 items-center justify-center rounded-full border border-sidebar-console-border bg-[#0F172A] hover:bg-slate-800 text-slate-400 hover:text-white transition-colors cursor-pointer shadow-md"
          title={isCollapsed ? "Expand Sidebar" : "Collapse Sidebar"}
        >
          {isCollapsed ? (
            <ChevronRight className="h-3.5 w-3.5" />
          ) : (
            <ChevronLeft className="h-3.5 w-3.5" />
          )}
        </button>
      </div>

      {/* [COMMENT]: Vùng scroll chứa các menu items. Hỗ trợ cuộn độc lập khi nội dung quá dài */}
      <div className="flex-1 overflow-y-auto overflow-x-hidden pt-6 pb-4 space-y-4 scrollbar-thin scrollbar-thumb-slate-800">
        {menuGroups.map((group, groupIndex) => (
          <div key={group.title} className="px-3">
            {/* [COMMENT]: Group Header theo chuẩn Enterprise (11px uppercase gray-500 letter-spacing) */}
            {!isCollapsed ? (
              <h3 className="px-3 mb-1.5 text-[11px] font-bold uppercase tracking-wider text-slate-500 select-none">
                {group.title}
              </h3>
            ) : (
              <div className="h-px bg-sidebar-console-border my-2 mx-1" />
            )}

            <div className="space-y-[4px]">
              {group.items.map((item) => {
                const IconComponent = item.icon;
                const isActive = activeId === item.id;

                // [COMMENT]: Khởi tạo phần tử item menu.
                // Nếu active: đổi background, tô sáng xanh, thêm thanh border trái 3px.
                // Nếu hover: đổi background nhạt mượt mà.
                const buttonContent = (
                  <button
                    onClick={() => setActiveId(item.id)}
                    className={cn(
                      "group/item relative flex h-10 w-full items-center gap-3 rounded-md px-3 text-sm font-medium transition-all duration-150 cursor-pointer select-none text-left outline-none",
                      isActive
                        ? "bg-sidebar-console-active-bg text-blue-400 font-semibold before:absolute before:left-0 before:top-[4px] before:bottom-[4px] before:w-[3px] before:rounded-r before:bg-sidebar-console-active-border"
                        : "text-slate-400 hover:bg-sidebar-console-hover hover:text-slate-200"
                    )}
                  >
                    <IconComponent
                      className={cn(
                        "h-4 w-4 shrink-0 transition-colors",
                        isActive ? "text-blue-400" : "text-slate-500 group-hover/item:text-slate-300"
                      )}
                    />
                    {!isCollapsed && (
                      <span className="truncate flex-1 animate-in fade-in duration-200">
                        {item.name}
                      </span>
                    )}

                    {/* [COMMENT]: Badge hiển thị số lượng thông báo hoặc sự cố. Tinh tế, không chói lọi */}
                    {item.badge && !isCollapsed && (
                      <span className="flex h-5 min-w-5 items-center justify-center rounded-full bg-blue-900/40 px-1.5 text-[10px] font-semibold text-blue-300 ring-1 ring-blue-500/20">
                        {item.badge}
                      </span>
                    )}
                    {item.badge && isCollapsed && (
                      <span className="absolute top-1.5 right-1.5 h-2 w-2 rounded-full bg-blue-500 animate-pulse" />
                    )}
                  </button>
                );

                // [COMMENT]: Khi collapse, bọc Tooltip xung quanh Icon để người dùng dễ nhận biết tính năng
                if (isCollapsed) {
                  return (
                    <Tooltip key={item.id}>
                      <TooltipTrigger render={buttonContent} />
                      <TooltipContent side="right" className="bg-[#1E293B] text-slate-100 border border-slate-700 text-xs">
                        <div className="flex items-center gap-2">
                          <span>{item.name}</span>
                          {item.badge && (
                            <span className="bg-blue-500 text-white text-[9px] px-1 rounded-full font-bold">
                              {item.badge}
                            </span>
                          )}
                        </div>
                      </TooltipContent>
                    </Tooltip>
                  );
                }

                return <div key={item.id}>{buttonContent}</div>;
              })}
            </div>
          </div>
        ))}
      </div>

      {/* [COMMENT]: Footer Sidebar. Mang phong cách của thiết bị Appliance chuyên nghiệp */}
      <div className="p-3 border-t border-sidebar-console-border bg-[#0B0F19]/50 shrink-0 select-none">
        <div className="flex flex-col gap-1.5">
          {/* [COMMENT]: Pulse Indicator cho Platform Health */}
          <div className="flex items-center gap-2">
            <span className="relative flex h-2 w-2 shrink-0">
              <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
              <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-500" />
            </span>
            {!isCollapsed && (
              <span className="text-[11px] font-medium text-emerald-400 truncate animate-in fade-in duration-200">
                Platform Healthy
              </span>
            )}
          </div>

          {!isCollapsed ? (
            <div className="mt-1 flex flex-col animate-in fade-in duration-200">
              <div className="flex items-center justify-between text-[11px] text-slate-400">
                <span className="font-semibold text-slate-300">System Admin</span>
                <span className="text-slate-500 font-mono">v1.4.2</span>
              </div>
              <span className="text-[10px] text-slate-500 truncate mt-0.5">
                phucle@aurora.cloud
              </span>
            </div>
          ) : (
            <div className="text-[10px] font-mono text-slate-500 text-center select-none pt-0.5">
              v1.4.2
            </div>
          )}
        </div>
      </div>
    </aside>
  );
}
