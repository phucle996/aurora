"use client";

import React, { useEffect, useState, useCallback } from "react";
import Link from "next/link";
import { Shield, RefreshCw, Key, ShieldAlert, Loader2, Plus } from "lucide-react";
import { toast } from "sonner";
import { listPlatformRoles, type PlatformRoleItem } from "@/lib/api/session";
import { cn } from "@/lib/utils";
import RouteGuard from "@/components/route-guard";
import { useUserSession } from "@/hooks/useUserSession";

// [COMMENT]: Trang AccessControlPage hiển thị danh sách vai trò (System Roles) phục vụ phân quyền RBAC
function AccessControlContent() {
  const [roles, setRoles] = useState<PlatformRoleItem[]>([]);
  const [loading, setLoading] = useState(true);
  const { checkPermission } = useUserSession();

  const canCreate = checkPermission("iam:role", "create");

  // [COMMENT]: Gọi API lấy danh sách Platform Roles
  const loadRoles = useCallback(async (showToast = false) => {
    setLoading(true);
    try {
      const data = await listPlatformRoles();
      setRoles(data);
      if (showToast) {
        toast.success("Platform roles synchronized.");
      }
    } catch (err) {
      console.error(err);
      toast.error(err instanceof Error ? err.message : "Failed to load roles list.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadRoles();
  }, [loadRoles]);

  return (
    <div className="space-y-6">
      {/* ========================================== */}
      {/* 1. Header Area */}
      {/* ========================================== */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 border-b border-slate-200 dark:border-slate-800 pb-5">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-slate-900 dark:text-slate-100 flex items-center gap-2">
            <Shield className="h-5 w-5 text-blue-500" />
            <span>Access Control (RBAC)</span>
          </h1>
          <p className="mt-1.5 text-xs font-semibold text-slate-500 dark:text-slate-400">
            View administrative system roles, authorization matrices, and permission hierarchies.
          </p>
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={() => void loadRoles(true)}
            disabled={loading}
            className="flex items-center justify-center h-8 px-3 rounded-lg border border-slate-200 dark:border-slate-800 hover:border-slate-300 dark:hover:border-slate-700 bg-white dark:bg-slate-900 text-xs font-semibold text-slate-600 dark:text-slate-300 hover:text-slate-950 dark:hover:text-slate-100 transition-colors disabled:opacity-50 cursor-pointer"
          >
            <RefreshCw className={cn("h-3.5 w-3.5 mr-1.5", loading ? "animate-spin" : "")} />
            <span>Sync</span>
          </button>

          {canCreate && (
            <Link
              href="/rbac/create"
              className="flex items-center justify-center h-8 px-3.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-xs font-semibold text-white transition-colors cursor-pointer shadow-xs"
            >
              <Plus className="h-3.5 w-3.5 mr-1.5" />
              <span>New Role</span>
            </Link>
          )}
        </div>
      </div>

      {/* ========================================== */}
      {/* 2. Main Content Card */}
      {/* ========================================== */}
      <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl overflow-hidden shadow-xs">
        {loading && roles.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 text-slate-500">
            <Loader2 className="h-7 w-7 animate-spin text-blue-500 mb-3" />
            <span className="text-xs font-semibold uppercase tracking-wider animate-pulse">Fetching roles...</span>
          </div>
        ) : roles.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 px-6 text-center text-slate-400 dark:text-slate-500 select-none">
            <ShieldAlert className="h-10 w-10 text-slate-300 dark:text-slate-700 mb-3" />
            <p className="text-sm font-semibold">No visible roles found</p>
            <p className="text-xs mt-1 max-w-xs text-slate-400 leading-normal">
              You either do not have permission clearance to read platform-level roles or connection was timed out.
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left border-collapse">
              <thead>
                <tr className="border-b border-slate-200 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-950/20 text-[10px] font-bold uppercase tracking-wider text-slate-500 dark:text-slate-400 select-none">
                  <th className="px-6 py-3.5">Role Name</th>
                  <th className="px-6 py-3.5">System Code</th>
                  <th className="px-6 py-3.5">Hierarchy Level</th>
                  <th className="px-6 py-3.5">Scope</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-150 dark:divide-slate-800 text-[13px]">
                {roles.map((r) => {
                  return (
                    <tr
                      key={r.id}
                      className="hover:bg-slate-50/40 dark:hover:bg-slate-800/10 transition-colors"
                    >
                      {/* Name */}
                      <td className="px-6 py-4 font-bold text-slate-800 dark:text-slate-200">
                        {r.name}
                      </td>

                      {/* Code */}
                      <td className="px-6 py-4 text-slate-600 dark:text-slate-400 font-mono text-xs">
                        {r.code}
                      </td>

                      {/* Role Level */}
                      <td className="px-6 py-4">
                        <span
                          className={cn(
                            "inline-flex items-center gap-1 px-2 py-0.5 rounded-md text-[10px] font-bold uppercase tracking-wider",
                            r.role_level === 0
                              ? "bg-rose-500/10 text-rose-500 border border-rose-500/15"
                              : r.role_level <= 10
                                ? "bg-amber-500/10 text-amber-500 border border-amber-500/15"
                                : "bg-blue-500/10 text-blue-500 border border-blue-500/15"
                          )}
                        >
                          <Key className="h-3 w-3 mr-0.5" />
                          <span>Level {r.role_level}</span>
                        </span>
                      </td>

                      {/* Scope */}
                      <td className="px-6 py-4 text-slate-500 dark:text-slate-500 font-semibold uppercase tracking-wider text-[10px]">
                        {r.scope}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

export default function AccessControlPage() {
  return (
    <RouteGuard requiredKey="iam:role" requiredAction="read">
      <AccessControlContent />
    </RouteGuard>
  );
}
