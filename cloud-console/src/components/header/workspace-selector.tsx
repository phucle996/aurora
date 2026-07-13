"use client";

import React, { useState, useRef, useEffect } from "react";
import { LayoutGrid, ChevronDown, Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { toast } from "sonner";
import { type WorkspaceCatalogItem } from "@/lib/api/workspace";

interface WorkspaceSelectorProps {
  catalog: WorkspaceCatalogItem[];
  activeWorkspaceID: string | null;
  selectWorkspace: (id: string) => void;
  loading: boolean;
}

export function WorkspaceSelector({
  catalog,
  activeWorkspaceID,
  selectWorkspace,
  loading
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
        className="flex items-center gap-1.5 text-xs font-semibold px-2.5 py-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-sidebar-console-hover text-slate-700 dark:text-slate-300 cursor-pointer transition-colors"
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
        <ChevronDown className="h-3 w-3 text-slate-400" />
      </button>

      {workspaceOpen && (
        <div className="absolute top-[110%] left-0 min-w-[200px] w-max max-w-[280px] bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-lg shadow-xl py-1 z-50 animate-in fade-in slide-in-from-top-1 duration-100">
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
