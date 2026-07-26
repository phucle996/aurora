"use client";

import React, { useMemo } from "react";
import { type PermissionItem } from "@/features/rbac/api";
import { cn } from "@/lib/utils";

interface SelectedPreviewCardProps {
  permissions: PermissionItem[];
  originalPerms: string[];
  selectedPerms: string[];
}

export default function SelectedPreviewCard({
  permissions,
  originalPerms,
  selectedPerms
}: SelectedPreviewCardProps) {
  // [COMMENT]: Tính toán số lượng thêm và xóa để hiển thị badge tóm tắt kiểu Git Diff
  const addedCount = useMemo(() => {
    return selectedPerms.filter((id) => !originalPerms.includes(id)).length;
  }, [originalPerms, selectedPerms]);

  const deletedCount = useMemo(() => {
    return originalPerms.filter((id) => !selectedPerms.includes(id)).length;
  }, [originalPerms, selectedPerms]);

  // [COMMENT]: Hợp tập hợp tất cả các quyền ban đầu và quyền hiện tại để dựng cây so sánh
  const unionTree = useMemo(() => {
    const unionIds = Array.from(new Set([...originalPerms, ...selectedPerms]));
    
    const root: Record<string, {
      name: string;
      objects: Record<string, {
        name: string;
        behaviors: { id: string; name: string; diffState: "added" | "deleted" | "unchanged" }[];
      }>;
    }> = {};

    unionIds.forEach((id) => {
      const perm = permissions.find((p) => p.id === id);
      if (!perm) return;
      
      const mod = perm.module || "other";
      const obj = perm.object || "default";

      // Phân tích trạng thái diff
      let diffState: "added" | "deleted" | "unchanged" = "unchanged";
      const wasOriginal = originalPerms.includes(id);
      const isSelected = selectedPerms.includes(id);
      
      if (isSelected && !wasOriginal) {
        diffState = "added";
      } else if (!isSelected && wasOriginal) {
        diffState = "deleted";
      }

      if (!root[mod]) {
        root[mod] = { name: mod, objects: {} };
      }
      if (!root[mod].objects[obj]) {
        root[mod].objects[obj] = { name: obj, behaviors: [] };
      }
      root[mod].objects[obj].behaviors.push({
        id,
        name: perm.behavior,
        diffState
      });
    });

    return root;
  }, [permissions, originalPerms, selectedPerms]);

  const totalDiffCount = addedCount + deletedCount;

  return (
    <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-5 shadow-xs space-y-4">
      <div className="border-b border-slate-200 dark:border-slate-800 pb-3 flex items-center justify-between">
        <h3 className="text-xs font-bold uppercase tracking-wider text-slate-855 dark:text-slate-200">
          Permissions Diff
        </h3>
        
        <div className="flex items-center gap-1.5">
          {addedCount > 0 && (
            <span className="text-[10px] font-bold text-emerald-600 bg-emerald-500/10 px-2 py-0.5 rounded border border-emerald-500/15">
              +{addedCount}
            </span>
          )}
          {deletedCount > 0 && (
            <span className="text-[10px] font-bold text-rose-600 bg-rose-500/10 px-2 py-0.5 rounded border border-rose-500/15">
              -{deletedCount}
            </span>
          )}
          <span className="text-[10px] font-bold text-blue-500 bg-blue-500/10 px-2 py-0.5 rounded border border-blue-500/15">
            {selectedPerms.length} Selected
          </span>
        </div>
      </div>

      {totalDiffCount === 0 && selectedPerms.length === 0 ? (
        <div className="text-center py-8 text-xs text-slate-400 dark:text-slate-500 font-semibold leading-relaxed">
          No permissions selected yet.<br />Toggles are available on the left tree.
        </div>
      ) : (
        <div className="space-y-3 max-h-120 overflow-y-auto pr-1">
          {Object.values(unionTree).map((modNode) => (
            <div key={modNode.name} className="space-y-1.5">
              <div className="text-xs font-bold font-mono text-blue-500 flex items-center gap-1.5">
                <span className="h-1.5 w-1.5 rounded-full bg-blue-500 animate-pulse" />
                <span>{modNode.name}</span>
              </div>

              <div className="pl-3.5 space-y-2 border-l border-slate-200 dark:border-slate-850 ml-0.5">
                {Object.values(modNode.objects).map((objNode) => (
                  <div key={objNode.name} className="space-y-1">
                    <div className="text-[11px] font-semibold font-mono text-slate-700 dark:text-slate-400">
                      {objNode.name}
                    </div>
                    <div className="flex flex-wrap gap-1.5">
                      {objNode.behaviors.map((b) => (
                        <span
                          key={b.id}
                          className={cn(
                            "text-[9px] font-bold font-mono px-1.5 py-0.5 rounded border transition-all select-none",
                            b.diffState === "added" && "bg-emerald-500/10 dark:bg-emerald-500/5 text-emerald-600 dark:text-emerald-400 border-emerald-500/25",
                            b.diffState === "deleted" && "bg-rose-500/10 dark:bg-rose-500/5 text-rose-550 dark:text-rose-400 border-rose-500/25 line-through opacity-70",
                            b.diffState === "unchanged" && "bg-slate-100 dark:bg-slate-800 text-slate-650 dark:text-slate-305 border-slate-200 dark:border-slate-700/50"
                          )}
                        >
                          {b.diffState === "added" ? `+ ${b.name}` : b.diffState === "deleted" ? `- ${b.name}` : b.name}
                        </span>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
