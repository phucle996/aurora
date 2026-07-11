"use client";

import React from "react";
import {
  FileJson,
  X,
  Upload,
  Search,
  ChevronDown,
  ChevronRight,
  CheckSquare,
  Square
} from "lucide-react";
import { type PermissionItem } from "@/lib/api/rbac";
import { cn } from "@/lib/utils";

interface GrantPermissionsCardProps {
  permissions: PermissionItem[];
  selectedPerms: string[];
  setSelectedPerms: React.Dispatch<React.SetStateAction<string[]>>;
  searchQuery: string;
  setSearchQuery: (v: string) => void;
  expandedModules: Record<string, boolean>;
  setExpandedModules: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  expandedObjects: Record<string, boolean>;
  setExpandedObjects: React.Dispatch<React.SetStateAction<Record<string, boolean>>>;
  showImport: boolean;
  setShowImport: (v: boolean) => void;
  importText: string;
  setImportText: (v: string) => void;
  handleFileUpload: (e: React.ChangeEvent<HTMLInputElement>) => void;
  handleImport: () => void;
  handleTogglePerm: (id: string) => void;
  tree: Record<string, {
    name: string;
    objects: Record<string, {
      name: string;
      behaviors: { id: string; behavior: string; description: string; fullName: string }[];
    }>;
  }>;
}

export default function GrantPermissionsCard({
  permissions,
  selectedPerms,
  setSelectedPerms,
  searchQuery,
  setSearchQuery,
  expandedModules,
  setExpandedModules,
  expandedObjects,
  setExpandedObjects,
  showImport,
  setShowImport,
  importText,
  setImportText,
  handleFileUpload,
  handleImport,
  handleTogglePerm,
  tree
}: GrantPermissionsCardProps) {
  return (
    <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-6 shadow-xs space-y-4">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-slate-150 dark:border-slate-800 pb-3">
        <h3 className="text-xs font-bold uppercase tracking-wider text-slate-850 dark:text-slate-200">
          Grant Permissions
        </h3>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setShowImport(!showImport)}
            className="flex items-center justify-center h-7 px-3.5 rounded-lg border border-slate-200 dark:border-slate-800 hover:border-slate-355 dark:hover:border-slate-700 text-[11px] font-semibold text-slate-650 dark:text-slate-350 transition-colors cursor-pointer"
          >
            <FileJson className="h-3 w-3 mr-1.5" />
            <span>Import Permissions</span>
          </button>
        </div>
      </div>

      {/* Import Textarea Panel */}
      {showImport && (
        <div className="bg-slate-50 dark:bg-slate-950/40 border border-slate-200 dark:border-slate-800 rounded-xl p-4 space-y-3.5">
          <div className="flex items-center justify-between">
            <span className="text-[10px] font-bold uppercase tracking-wider text-slate-400 dark:text-slate-500">
              Import Permission Keys
            </span>
            <button
              type="button"
              onClick={() => setShowImport(false)}
              className="p-1 rounded text-slate-400 hover:bg-slate-200 dark:hover:bg-slate-800 cursor-pointer"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
          <p className="text-[10px] text-slate-455 dark:text-slate-500 leading-normal">
            Paste a comma-separated list of keys, newlines, or a JSON array. Wildcards are supported. For example: <code className="bg-slate-200/50 dark:bg-slate-900 px-1 py-0.5 rounded">iam:users:*</code>, <code className="bg-slate-200/50 dark:bg-slate-900 px-1 py-0.5 rounded">compute:vps:read</code>.
          </p>
          <textarea
            rows={4}
            value={importText}
            onChange={(e) => setImportText(e.target.value)}
            placeholder='["compute:vps:*", "iam:*:read", "billing:invoice:read"]'
            className="w-full px-3 py-2 rounded-lg border border-slate-200 dark:border-slate-855 bg-white dark:bg-slate-900 text-xs font-mono text-slate-900 dark:text-slate-100 placeholder-slate-400 dark:placeholder-slate-600 focus:outline-hidden focus:border-blue-500 dark:focus:border-blue-500 resize-none"
          />
          <div className="flex items-center justify-between gap-3 pt-1">
            <label className="flex items-center justify-center h-7 px-3.5 rounded-lg border border-slate-200 dark:border-slate-800 hover:bg-slate-55 dark:hover:bg-slate-850 text-[11px] font-semibold text-slate-605 dark:text-slate-350 transition-colors cursor-pointer gap-1.5">
              <Upload className="h-3.5 w-3.5" />
              <span>Upload JSON</span>
              <input
                type="file"
                accept=".json,.txt"
                onChange={handleFileUpload}
                className="hidden"
              />
            </label>
            <button
              type="button"
              onClick={handleImport}
              className="flex items-center justify-center h-7 px-4 rounded-lg bg-blue-600 hover:bg-blue-700 text-[11px] font-semibold text-white transition-colors cursor-pointer shadow-xs"
            >
              Apply Import
            </button>
          </div>
        </div>
      )}

      {/* Filtering Toolbar */}
      <div className="relative">
        <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-slate-400 pointer-events-none" />
        <input
          type="text"
          placeholder="Filter by keyword (e.g. compute:vps)..."
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          className="h-8 w-full pl-8 pr-3 rounded-lg border border-slate-200 dark:border-slate-800 bg-transparent text-xs text-foreground placeholder-slate-455 focus:outline-hidden focus:border-blue-500 transition-colors"
        />
      </div>

      {/* Permissions Tree Render */}
      {permissions.length === 0 ? (
        <div className="text-center py-10 text-slate-400">No permissions loaded.</div>
      ) : Object.keys(tree).length === 0 ? (
        <div className="text-center py-10 text-slate-400 dark:text-slate-600 text-xs font-semibold select-none">
          No permissions match your keyword filter.
        </div>
      ) : (
        <div className="space-y-4 select-none">
          {Object.values(tree).map((modNode) => {
            const isModExpanded = !!expandedModules[modNode.name];

            const modBehaviors: string[] = [];
            Object.values(modNode.objects).forEach((o) => {
              o.behaviors.forEach((b) => modBehaviors.push(b.id));
            });

            const modSelectedCount = modBehaviors.filter((id) => selectedPerms.includes(id)).length;
            const isModAllSelected = modSelectedCount === modBehaviors.length && modBehaviors.length > 0;
            const isModSomeSelected = modSelectedCount > 0 && !isModAllSelected;

            return (
              <div
                key={modNode.name}
                className="border border-slate-200 dark:border-slate-800/80 rounded-xl p-3.5 bg-slate-50/20 dark:bg-slate-955/5 space-y-3"
              >
                {/* Module Header Row */}
                <div className="flex items-center justify-between pb-1 border-b border-slate-150 dark:border-slate-850/60">
                  <div className="flex items-center gap-1.5">
                    <button
                      type="button"
                      onClick={() =>
                        setExpandedModules((prev) => ({ ...prev, [modNode.name]: !prev[modNode.name] }))
                      }
                      className="p-0.5 hover:bg-slate-200 dark:hover:bg-slate-800 rounded text-slate-455 hover:text-slate-700 dark:hover:text-slate-200 transition-colors cursor-pointer"
                    >
                      {isModExpanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                    </button>

                    <button
                      type="button"
                      onClick={() => {
                        if (isModAllSelected) {
                          setSelectedPerms((prev) => prev.filter((id) => !modBehaviors.includes(id)));
                        } else {
                          setSelectedPerms((prev) => {
                            const filtered = prev.filter((id) => !modBehaviors.includes(id));
                            return [...filtered, ...modBehaviors];
                          });
                        }
                      }}
                      className="flex items-center text-slate-550 dark:text-slate-400 cursor-pointer"
                    >
                      {isModAllSelected ? (
                        <CheckSquare className="h-4 w-4 text-blue-500 mr-1.5" />
                      ) : isModSomeSelected ? (
                        <div className="h-4 w-4 border border-blue-500 rounded bg-blue-500/20 mr-1.5 flex items-center justify-center">
                          <div className="h-0.5 w-2 bg-blue-500" />
                        </div>
                      ) : (
                        <Square className="h-4 w-4 mr-1.5" />
                      )}
                    </button>

                    <span className="text-xs font-bold text-slate-900 dark:text-slate-100 uppercase tracking-wider font-mono">
                      {modNode.name}
                    </span>
                  </div>

                  <span className="text-[10px] text-slate-400 dark:text-slate-500 font-bold">
                    {modSelectedCount} / {modBehaviors.length} selected
                  </span>
                </div>

                {/* Objects container (Level 2) */}
                {isModExpanded && (
                  <div className="space-y-4 pt-1">
                    {Object.values(modNode.objects).map((objNode) => {
                      const uniqueKey = `${modNode.name}:${objNode.name}`;
                      const isObjExpanded = !!expandedObjects[uniqueKey];

                      const objBehaviors = objNode.behaviors.map((b) => b.id);
                      const objSelectedCount = objBehaviors.filter((id) => selectedPerms.includes(id)).length;
                      const isObjAllSelected = objSelectedCount === objBehaviors.length && objBehaviors.length > 0;
                      const isObjSomeSelected = objSelectedCount > 0 && !isObjAllSelected;

                      return (
                        <div
                          key={objNode.name}
                          className="ml-1.5 border-l-2 border-slate-200 dark:border-slate-800 pl-3.5 space-y-2"
                        >
                          {/* Object Header Row */}
                          <div className="flex items-center gap-1.5 py-0.5">
                            <button
                              type="button"
                              onClick={() =>
                                setExpandedObjects((prev) => ({ ...prev, [uniqueKey]: !prev[uniqueKey] }))
                              }
                              className="p-0.5 hover:bg-slate-200 dark:hover:bg-slate-855 rounded text-slate-400 hover:text-slate-655 transition-colors cursor-pointer"
                            >
                              {isObjExpanded ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                            </button>

                            <button
                              type="button"
                              onClick={() => {
                                if (isObjAllSelected) {
                                  setSelectedPerms((prev) => prev.filter((id) => !objBehaviors.includes(id)));
                                } else {
                                  setSelectedPerms((prev) => {
                                    const filtered = prev.filter((id) => !objBehaviors.includes(id));
                                    return [...filtered, ...objBehaviors];
                                  });
                                }
                              }}
                              className="flex items-center text-slate-550 dark:text-slate-400 cursor-pointer"
                            >
                              {isObjAllSelected ? (
                                <CheckSquare className="h-3.5 w-3.5 text-blue-500 mr-1.5" />
                              ) : isObjSomeSelected ? (
                                <div className="h-3.5 w-3.5 border border-blue-500 rounded bg-blue-500/20 mr-1.5 flex items-center justify-center">
                                  <div className="h-0.5 w-1.5 bg-blue-500" />
                                </div>
                              ) : (
                                <Square className="h-3.5 w-3.5 mr-1.5" />
                              )}
                            </button>

                            <span className="text-xs font-bold text-slate-700 dark:text-slate-300 font-mono">
                              {objNode.name}
                            </span>
                            <span className="text-[10px] text-slate-400 dark:text-slate-505 font-semibold">
                              ({objSelectedCount}/{objBehaviors.length})
                            </span>
                          </div>

                          {/* Behaviors grid checkboxes (Level 3) */}
                          {isObjExpanded && (
                            <div className="ml-5 grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-2 pt-1 pb-1.5">
                              {objNode.behaviors.map((b) => {
                                const isChecked = selectedPerms.includes(b.id);
                                return (
                                  <div
                                    key={b.id}
                                    onClick={() => handleTogglePerm(b.id)}
                                    className={`flex items-start gap-2 p-2 rounded-lg border transition-all select-none group cursor-pointer ${
                                      isChecked
                                        ? "bg-blue-500/10 border-blue-500/30 text-blue-200"
                                        : "bg-white dark:bg-slate-900/40 border-slate-200 dark:border-slate-800/80 hover:border-slate-355 dark:hover:border-slate-705"
                                    }`}
                                  >
                                    <div className="mt-0.5 text-slate-400 group-hover:text-blue-500 transition-colors">
                                      {isChecked ? (
                                        <CheckSquare className="h-3.5 w-3.5 text-blue-500" />
                                      ) : (
                                        <Square className="h-3.5 w-3.5" />
                                      )}
                                    </div>
                                    <div className="space-y-0.5 leading-none">
                                      <span className="text-[11px] font-bold font-mono text-slate-800 dark:text-slate-200 group-hover:text-blue-400 transition-colors">
                                        {b.behavior}
                                      </span>
                                      {b.description && (
                                        <p className="text-[9px] text-slate-400 dark:text-slate-500 font-medium leading-normal">
                                          {b.description}
                                        </p>
                                      )}
                                    </div>
                                  </div>
                                );
                              })}
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
