"use client";

import React, { useState, useRef, useEffect } from "react";
import { LayoutGrid, ChevronDown, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import { type WorkspaceCatalogItem } from "@/features/workspaces/api";

interface WorkspaceSelectorProps {
  catalog: WorkspaceCatalogItem[];
  activeWorkspaceID: string | null;
  selectWorkspace: (id: string) => void;
  loading: boolean;
  compact?: boolean;
}

export function WorkspaceSelector({
  catalog,
  activeWorkspaceID,
  selectWorkspace,
  loading,
  compact = false,
}: WorkspaceSelectorProps) {
  const [workspaceOpen, setWorkspaceOpen] = useState(false);
  const workspaceRef = useRef<HTMLDivElement>(null);

  const activeWorkspace = catalog.find((w) => w.id === activeWorkspaceID);

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (workspaceRef.current && !workspaceRef.current.contains(e.target as Node)) {
        setWorkspaceOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, []);

  return (
    <div ref={workspaceRef} className="relative">
      <button
        onClick={() => setWorkspaceOpen(!workspaceOpen)}
        className={cn(
          "flex items-center gap-1.5 rounded-[4px] px-2.5 py-1.5 text-xs font-semibold text-slate-700 outline-none transition-colors hover:bg-slate-100 focus-visible:ring-2 focus-visible:ring-blue-500 dark:text-slate-300 dark:hover:bg-sidebar-console-hover",
          compact && "w-full justify-start text-slate-300 hover:bg-sidebar-console-hover",
        )}
      >
        <LayoutGrid className="h-3 w-3 text-slate-400 dark:text-slate-500 shrink-0" />
        <span className="text-slate-400 dark:text-slate-500 font-normal">Workspace:</span>
        {loading ? (
          <Loader2 className="h-3 w-3 animate-spin text-slate-400" />
        ) : (
          <span className="max-w-[120px] truncate">
            {activeWorkspace?.name ?? "Select..."}
          </span>
        )}
        <ChevronDown className={cn("h-3 w-3 text-slate-400", compact && "ml-auto")} />
      </button>

      {workspaceOpen && (
        <div className="absolute left-0 top-[110%] z-50 w-max min-w-[200px] max-w-[280px] rounded-[6px] border border-slate-200 bg-white py-1 shadow-sm dark:border-slate-800 dark:bg-slate-900">
          <div className="px-3 py-1.5 border-b border-slate-100 dark:border-slate-800 mb-1">
            <span className="text-[10px] font-bold uppercase tracking-wider text-slate-400 dark:text-slate-500">
              Your Workspaces
            </span>
          </div>

          {catalog.length === 0 && !loading && (
            <div className="px-3 py-3 text-xs text-slate-400 dark:text-slate-500 text-center">
              No workspaces available
            </div>
          )}

          {loading && (
            <div className="flex items-center justify-center py-3 gap-2 text-xs text-slate-400">
              <Loader2 className="h-3 w-3 animate-spin" />
              Loading...
            </div>
          )}

          {catalog.map((ws) => {
            const isActive = ws.id === activeWorkspaceID;
            return (
              <button
                key={ws.id}
                onClick={() => {
                  selectWorkspace(ws.id);
                  setWorkspaceOpen(false);
                  toast.success(`Workspace: ${ws.name}`);
                }}
                className={cn(
                  "w-full text-left px-3 py-2 hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer transition-colors",
                  isActive && "bg-slate-50 dark:bg-slate-800/40"
                )}
              >
                <div className={cn(
                  "text-xs font-semibold leading-tight",
                  isActive ? "text-blue-500 dark:text-blue-400" : "text-slate-700 dark:text-slate-300"
                )}>
                  {ws.name}
                  {isActive && (
                    <span className="ml-1.5 inline-block h-1.5 w-1.5 rounded-full bg-blue-500 align-middle" />
                  )}
                </div>
                <div className="text-[10px] text-slate-400 font-mono mt-0.5">{ws.code}</div>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
