"use client";

import React, { useState, useEffect, useRef } from "react";
import { Search, Terminal, Cpu, Database, Network, Server, UserCheck, ShieldAlert, FileText, Globe } from "lucide-react";
import { cn } from "@/lib/utils";

// [COMMENT]: Định nghĩa cấu trúc Command Item trong Command Palette
interface CommandItem {
  id: string;
  name: string;
  category: string;
  icon: React.ComponentType<{ className?: string }>;
  shortcut?: string;
  action: () => void;
}

interface CommandPaletteProps {
  isOpen: boolean;
  setIsOpen: (open: boolean) => void;
  setActivePage: (id: string) => void;
}

export default function CommandPalette({
  isOpen,
  setIsOpen,
  setActivePage
}: CommandPaletteProps) {
  const [search, setSearch] = useState("");
  const [selectedIndex, setSelectedIndex] = useState(0);

  const modalRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  // [COMMENT]: Các lệnh điều hướng nhanh và hành động trên Control Plane
  const commands: CommandItem[] = [
    {
      id: "go-overview",
      name: "Go to System Overview",
      category: "Navigation",
      icon: Terminal,
      shortcut: "G O",
      action: () => {
        setActivePage("overview");
        setIsOpen(false);
      }
    },
    {
      id: "go-zones",
      name: "Manage Availability Zones",
      category: "Navigation",
      icon: Globe,
      shortcut: "G Z",
      action: () => {
        setActivePage("zones");
        setIsOpen(false);
      }
    },
    {
      id: "go-vms",
      name: "Provision and Manage VMs",
      category: "Navigation",
      icon: Server,
      shortcut: "G V",
      action: () => {
        setActivePage("vms");
        setIsOpen(false);
      }
    },
    {
      id: "go-incidents",
      name: "View Active Incident Reports",
      category: "Operations",
      icon: ShieldAlert,
      shortcut: "G I",
      action: () => {
        setActivePage("incidents");
        setIsOpen(false);
      }
    },
    {
      id: "go-hypervisors",
      name: "Show Hypervisors CPU/RAM",
      category: "Navigation",
      icon: Cpu,
      action: () => {
        setActivePage("hypervisors");
        setIsOpen(false);
      }
    },
    {
      id: "go-storage",
      name: "Storage Pools & CEPH status",
      category: "Navigation",
      icon: Database,
      action: () => {
        setActivePage("storage");
        setIsOpen(false);
      }
    },
    {
      id: "go-networks",
      name: "Network subnets & VLAN layout",
      category: "Navigation",
      icon: Network,
      action: () => {
        setActivePage("networks");
        setIsOpen(false);
      }
    },
    {
      id: "go-iam",
      name: "Manage Identity & Access Control (IAM)",
      category: "Security",
      icon: UserCheck,
      action: () => {
        setActivePage("iam");
        setIsOpen(false);
      }
    },
    {
      id: "open-docs",
      name: "Search Kubernetes API docs",
      category: "Documentation",
      icon: FileText,
      action: () => {
        window.open("#", "_blank");
        setIsOpen(false);
      }
    }
  ];

  // [COMMENT]: Lọc danh sách câu lệnh dựa trên input của người dùng
  const filteredCommands = commands.filter((cmd) =>
    cmd.name.toLowerCase().includes(search.toLowerCase()) ||
    cmd.category.toLowerCase().includes(search.toLowerCase())
  );

  // [COMMENT]: Lắng nghe tổ hợp phím tắt Ctrl+K / Cmd+K để bật/tắt Command Palette
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === "k") {
        e.preventDefault();
        setIsOpen(!isOpen);
      }
      if (e.key === "Escape") {
        setIsOpen(false);
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, setIsOpen]);

  // [COMMENT]: Tự động focus vào ô tìm kiếm khi mở Command Palette
  useEffect(() => {
    if (isOpen) {
      setSearch("");
      setSelectedIndex(0);
      setTimeout(() => inputRef.current?.focus(), 50);
    }
  }, [isOpen]);

  // [COMMENT]: Di chuyển tiêu điểm tìm kiếm bằng phím Arrow Up/Down và nhấn Enter để thực thi
  useEffect(() => {
    const handleNavigation = (e: KeyboardEvent) => {
      if (!isOpen) return;

      if (e.key === "ArrowDown") {
        e.preventDefault();
        setSelectedIndex((prev) => (prev + 1) % Math.max(1, filteredCommands.length));
      } else if (e.key === "ArrowUp") {
        e.preventDefault();
        setSelectedIndex((prev) => (prev - 1 + filteredCommands.length) % Math.max(1, filteredCommands.length));
      } else if (e.key === "Enter") {
        e.preventDefault();
        if (filteredCommands[selectedIndex]) {
          filteredCommands[selectedIndex].action();
        }
      }
    };

    window.addEventListener("keydown", handleNavigation);
    return () => window.removeEventListener("keydown", handleNavigation);
  }, [isOpen, filteredCommands, selectedIndex]);

  // [COMMENT]: Xử lý sự kiện click ra ngoài vùng modal để đóng palette
  useEffect(() => {
    const handleOutsideClick = (e: MouseEvent) => {
      if (modalRef.current && !modalRef.current.contains(e.target as Node)) {
        setIsOpen(false);
      }
    };

    if (isOpen) {
      document.addEventListener("mousedown", handleOutsideClick);
    }
    return () => document.removeEventListener("mousedown", handleOutsideClick);
  }, [isOpen, setIsOpen]);

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-[15vh] bg-slate-950/60 backdrop-blur-xs select-none">
      <div
        ref={modalRef}
        className="w-full max-w-xl bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl shadow-2xl overflow-hidden animate-in fade-in zoom-in-95 duration-150"
      >
        {/* [COMMENT]: Ô Input tìm kiếm có icon đính kèm */}
        <div className="flex items-center gap-3 px-4 py-3 border-b border-slate-100 dark:border-slate-800">
          <Search className="h-4 w-4 text-slate-400 shrink-0" />
          <input
            ref={inputRef}
            type="text"
            className="w-full bg-transparent border-0 text-sm outline-none text-slate-800 dark:text-slate-100 placeholder-slate-400"
            placeholder="Type a command or search resource..."
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setSelectedIndex(0);
            }}
          />
          <span className="text-[10px] bg-slate-100 dark:bg-slate-800 text-slate-500 px-1.5 py-0.5 rounded font-mono border border-slate-200 dark:border-slate-700/60 shadow-xs">
            ESC
          </span>
        </div>

        {/* [COMMENT]: Vùng hiển thị kết quả tìm kiếm */}
        <div className="max-h-[320px] overflow-y-auto p-2">
          {filteredCommands.length > 0 ? (
            <div className="space-y-0.5">
              {filteredCommands.map((cmd, idx) => {
                const IconComponent = cmd.icon;
                const isSelected = idx === selectedIndex;

                return (
                  <button
                    key={cmd.id}
                    onClick={() => cmd.action()}
                    className={cn(
                      "w-full flex items-center justify-between px-3 py-2.5 rounded-lg text-left text-xs font-medium cursor-pointer transition-colors outline-none",
                      isSelected
                        ? "bg-slate-100 dark:bg-slate-800 text-slate-900 dark:text-slate-100"
                        : "text-slate-600 dark:text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-800/30"
                    )}
                  >
                    <div className="flex items-center gap-2.5 min-w-0">
                      <IconComponent className={cn("h-4 w-4 shrink-0", isSelected ? "text-blue-500" : "text-slate-400")} />
                      <div className="truncate">
                        <span className="text-slate-900 dark:text-slate-200 font-semibold text-xs mr-2">
                          [{cmd.category}]
                        </span>
                        <span>{cmd.name}</span>
                      </div>
                    </div>
                    {cmd.shortcut && (
                      <span className="text-[9px] font-mono bg-slate-200 dark:bg-slate-700 text-slate-500 dark:text-slate-400 px-1.5 py-0.5 rounded shadow-xs shrink-0">
                        {cmd.shortcut}
                      </span>
                    )}
                  </button>
                );
              })}
            </div>
          ) : (
            <div className="py-8 text-center text-xs text-slate-400 flex flex-col items-center gap-2">
              <Terminal className="h-6 w-6 text-slate-500" />
              <span>No commands found matching "{search}"</span>
            </div>
          )}
        </div>

        {/* [COMMENT]: Footer chỉ dẫn phím tắt chuyển hướng */}
        <div className="px-4 py-2 bg-slate-50 dark:bg-slate-800/40 border-t border-slate-100 dark:border-slate-800/60 flex items-center justify-between text-[10px] text-slate-400">
          <div className="flex items-center gap-3">
            <span>↑↓ to navigate</span>
            <span>↵ to select</span>
          </div>
          <span>Aurora Cloud Control Plane</span>
        </div>
      </div>
    </div>
  );
}
