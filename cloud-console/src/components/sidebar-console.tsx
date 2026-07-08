"use client";

import React from "react";
import { useRouter, usePathname } from "next/navigation";
import {
  LayoutGrid,
  Lock,
  Users,
  ChevronLeft,
  ChevronRight,
} from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { useUserSession } from "@/hooks/useUserSession";

// [COMMENT]: Định nghĩa cấu trúc Item trong Sidebar. 
// Đảm bảo kiểu dữ liệu chặt chẽ cho từng Domain và Menu Item.
interface SidebarItem {
  id: string;
  name: string;
  icon: React.ComponentType<{ className?: string }>;
  badge?: number;
  path?: string;
}

interface SidebarGroup {
  title: string;
  items: SidebarItem[];
}

// [COMMENT]: SidebarItemCandidate mở rộng SidebarItem với thông tin kiểm quyền
// (được dùng nội bộ trong useMemo, không expose ra bên ngoài).
interface SidebarItemCandidate extends SidebarItem {
  matchKey: string;
  requiredAction: string;
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
  const { renderContext } = useUserSession();
  const router = useRouter();
  const pathname = usePathname();

  // [COMMENT]: hasAccess — console là render engine thuần túy.
  // Backend quyết định navigation nào trả về, frontend chỉ kiểm tra xem
  // key và action có tồn tại trong renderContext hay không.
  // Không có logic đặc biệt phía client (superadmin, wildcard tự thêm, v.v.).
  const hasAccess = React.useCallback((matchKey: string, action: string): boolean => {
    const navs = renderContext?.navigation;
    if (!navs) return false;

    const matchParts = matchKey.split(":");
    if (matchParts.length !== 4) return false;

    return navs.some(nav => {
      const navParts = nav.key.split(":");
      if (navParts.length !== 4) return false;
      // So khớp từng phần: "*" trong matchKey chấp nhận bất kỳ giá trị nào từ nav key
      const keyMatch = matchParts.every((part, i) => part === "*" || part === navParts[i]);
      if (!keyMatch) return false;
      // Kiểm tra action tồn tại trong danh sách actions backend cấp
      return nav.actions.includes(action);
    });
  }, [renderContext]);

  // [COMMENT]: Detect if current context is Personal (Bậc 1 is not a UUID)
  const isPersonal = React.useMemo(() => {
    const navs = renderContext?.navigation;
    if (!navs || navs.length === 0) return true;
    const firstKey = navs[0].key;
    const parts = firstKey.split(":");
    if (parts.length > 0) {
      const isUuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(parts[0]);
      return !isUuid;
    }
    return true;
  }, [renderContext]);

  // [COMMENT]: Map các menu động từ renderContext nhận từ API
  const menuGroups = React.useMemo(() => {
    if (!renderContext?.navigation) return [];

    // [COMMENT]: allItems typed rõ ràng là SidebarItemCandidate[] để TypeScript
    // kiểm tra đầy đủ các field matchKey và requiredAction.
    const allItems: SidebarItemCandidate[] = [
      {
        id: "workspaces",
        name: isPersonal ? "My Workspaces" : "Workspaces",
        icon: LayoutGrid,
        path: "/workspaces",
        matchKey: isPersonal ? "*:*:hierarchy:workspace" : "*:*:tenant:workspaces",
        requiredAction: isPersonal ? "read" : "list"
      },
      {
        id: "users",
        name: "User Directory",
        icon: Users,
        path: "/users",
        matchKey: "*:*:iam:users",
        // [COMMENT]: Chỉ render khi user có quyền read trên iam:users
        requiredAction: "read"
      },
      {
        id: "role",
        name: "Access Control (RBAC)",
        icon: Lock,
        path: "/rbac",
        matchKey: "*:*:iam:role",
        // [COMMENT]: Chỉ render khi user có quyền read trên iam:role
        requiredAction: "read"
      }
    ];


    // [COMMENT]: Tất cả items trong cloud-console đều là console items —
    // không có phân biệt "admin" vs "console". Lọc theo quyền rồi đưa vào
    // một group duy nhất.
    const visibleItems: SidebarItem[] = allItems.filter((item) =>
      hasAccess(item.matchKey, item.requiredAction)
    );

    if (visibleItems.length === 0) return [];

    return [
      {
        title: "Console",
        items: visibleItems,
      },
    ];
  }, [renderContext, hasAccess]);

  return (
    <aside
      className={cn(
        "fixed top-0 bottom-0 left-0 z-50 flex flex-col bg-sidebar-console-bg text-slate-200 border-r border-sidebar-console-border transition-all duration-300 ease-in-out select-none",
        isCollapsed ? "w-[60px]" : "w-[272px]"
      )}
    >
      {/* [COMMENT]: Header Logo area. Chiều cao chuẩn 64px, logo sử dụng bo góc 4px chuyên nghiệp */}
      <div className="flex h-16 items-center justify-between px-4 border-b border-sidebar-console-border relative shrink-0">
        <div className="flex items-center gap-3 overflow-hidden">
          <div className="flex h-8 w-8 items-center justify-center rounded-[4px] bg-blue-600 shrink-0 text-white font-bold text-lg">
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
      </div>

      {/* [COMMENT]: Vùng scroll chứa các menu items. Spacing được rút gọn (space-y-3) để tăng mật độ thông tin */}
      <div className="flex-1 overflow-y-auto overflow-x-hidden pt-4 pb-4 space-y-3 scrollbar-thin scrollbar-thumb-slate-800">
        {menuGroups.map((group, groupIndex) => (
          <div key={group.title} className="px-3">
            {/* [COMMENT]: Group Header theo chuẩn Enterprise (11px uppercase gray-500, khoảng cách lề mb-1 hẹp hơn) */}
            {!isCollapsed ? (
              <h3 className="px-3 mb-1 text-[10px] font-bold uppercase tracking-wider text-slate-500 select-none">
                {group.title}
              </h3>
            ) : (
              <div className="h-px bg-sidebar-console-border my-2 mx-1" />
            )}

            <div className="space-y-[2px]">
              {group.items.map((item) => {
                const IconComponent = item.icon;
                const isActive = activeId === item.id;

                // [COMMENT]: Khởi tạo phần tử item menu với chiều cao rút gọn h-8, font size text-xs và bo góc 4px (rounded-[4px]).
                // Thanh line chỉ báo active được điều chỉnh top/bottom thành 2px để ôm khít chiều cao mới.
                const buttonContent = (
                  <button
                    onClick={() => {
                      if (item.path && item.path !== pathname) {
                        router.push(item.path);
                      } else {
                        setActiveId(item.id);
                      }
                    }}
                    className={cn(
                      "group/item relative flex h-8 w-full items-center gap-2.5 rounded-[4px] px-2.5 text-[13px] font-medium transition-all duration-150 cursor-pointer select-none text-left outline-none",
                      isActive
                        ? "bg-sidebar-console-active-bg text-blue-400 font-semibold before:absolute before:left-0 before:top-[2px] before:bottom-[2px] before:w-[3px] before:rounded-r before:bg-sidebar-console-active-border"
                        : "text-slate-400 hover:bg-sidebar-console-hover hover:text-slate-200"
                    )}
                  >
                    <IconComponent
                      className={cn(
                        "h-3.5 w-3.5 shrink-0 transition-colors",
                        isActive ? "text-blue-400" : "text-slate-500 group-hover/item:text-slate-300"
                      )}
                    />
                    {!isCollapsed && (
                      <span className="truncate flex-1 animate-in fade-in duration-200">
                        {item.name}
                      </span>
                    )}

                    {/* [COMMENT]: Badge số lượng rút gọn nhỏ hơn, sử dụng bo góc 4px (rounded-[4px]) để đồng bộ */}
                    {item.badge && !isCollapsed && (
                      <span className="flex h-4 min-w-4 items-center justify-center rounded-[4px] bg-blue-900/45 px-1 text-[11px] font-bold text-blue-300 ring-1 ring-blue-500/20">
                        {item.badge}
                      </span>
                    )}
                    {item.badge && isCollapsed && (
                      <span className="absolute top-1.5 right-1.5 h-1.5 w-1.5 rounded-full bg-blue-500 animate-pulse" />
                    )}
                  </button>
                );

                // [COMMENT]: Khi collapse, bọc Tooltip xung quanh Icon để người dùng dễ nhận biết tính năng
                if (isCollapsed) {
                  return (
                    <Tooltip key={item.id}>
                      <TooltipTrigger render={buttonContent} />
                      <TooltipContent side="right" className="bg-[#1E293B] text-slate-100 border border-slate-700 text-xs rounded-[4px]">
                        <div className="flex items-center gap-2">
                          <span>{item.name}</span>
                          {item.badge && (
                            <span className="bg-blue-500 text-white text-[10px] px-1 rounded-[3px] font-bold">
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
