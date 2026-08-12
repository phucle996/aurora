"use client";

import React, { useState } from "react";
import Link from "next/link";
import {
  Shield,
  RefreshCw,
  ShieldAlert,
  Loader2,
  Plus,
  Copy,
  Check,
  Search,
  Settings,
  User,
  Trash2
} from "lucide-react";
import { toast } from "sonner";
import { listRoles, deleteRole, type PlatformRoleItem } from "@/features/rbac/api";
import { cn } from "@/lib/utils";
import { useUserSession } from "@/session/use-session";
import { useWorkspace } from "@/context/WorkspaceContext";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useConsoleQueryScope } from "@/shared/query/scope";

// [COMMENT]: Component CopyBadge giúp copy nhanh code của vai trò với phong cách Monospace tinh tế
function CopyBadge({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const handleCopy = (e: React.MouseEvent) => {
    e.stopPropagation();
    navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <button
      onClick={handleCopy}
      className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-slate-50 hover:bg-slate-100 dark:bg-slate-900/40 dark:hover:bg-slate-800 text-slate-600 dark:text-slate-400 border border-slate-200/60 dark:border-slate-800/80 text-[10px] font-mono transition-colors group select-all"
    >
      <span>{value}</span>
      {copied ? (
        <Check className="h-3 w-3 text-emerald-500" />
      ) : (
        <Copy className="h-3 w-3 text-slate-400 group-hover:text-slate-600 dark:group-hover:text-slate-300 transition-colors" />
      )}
    </button>
  );
}

// [COMMENT]: Định dạng ngày tháng hiển thị thông minh "Today", "Yesterday", "MMM DD"
function formatDate(dateStr: string): string {
  try {
    const d = new Date(dateStr);
    if (isNaN(d.getTime())) return dateStr;
    const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
    const now = new Date();
    if (d.toDateString() === now.toDateString()) {
      return "Today";
    }
    const yesterday = new Date();
    yesterday.setDate(now.getDate() - 1);
    if (d.toDateString() === yesterday.toDateString()) {
      return "Yesterday";
    }
    return `${months[d.getMonth()]} ${d.getDate().toString().padStart(2, "0")}`;
  } catch {
    return dateStr;
  }
}

// [COMMENT]: Hàm map metadata bổ trợ icon và fallback description cho từng vai trò
const getRoleIcon = (code: string) => {
  switch (code) {
    case "platform_root":
      return {
        icon: Shield,
        iconColor: "text-blue-500 bg-blue-500/10",
        fallbackDesc: "Root platform administrator with unrestricted superuser access.",
      };
    case "platform_admin":
      return {
        icon: Shield,
        iconColor: "text-indigo-500 bg-indigo-500/10",
        fallbackDesc: "System administrator with full read/write privileges across resources.",
      };
    case "platform_support_operator":
      return {
        icon: Settings,
        iconColor: "text-amber-500 bg-amber-500/10",
        fallbackDesc: "Support staff with read-only audit and basic support operation capabilities.",
      };
    default:
      return {
        icon: User,
        iconColor: "text-slate-500 bg-slate-500/10",
        fallbackDesc: "Custom role created for specific workspace execution policies.",
      };
  }
};

// [COMMENT]: Trang AccessControlPage hiển thị danh sách vai trò (System Roles) phục vụ phân quyền RBAC
export function AccessControlScreen() {
  const queryClient = useQueryClient();
  const { checkPermission } = useUserSession();
  const scope = useConsoleQueryScope();
  const { activeWorkspaceID, loading: workspaceLoading } = useWorkspace();
  const workspaceReady = !workspaceLoading && Boolean(activeWorkspaceID);

  // Local Filtering states
  const [searchQuery, setSearchQuery] = useState("");
  const [scopeFilter, setScopeFilter] = useState("all");
  const [levelFilter, setLevelFilter] = useState("all");

  const canCreate = checkPermission("iam:role", "write");
  const canDelete = checkPermission("iam:role", "delete");

  const [roleToDelete, setRoleToDelete] = useState<PlatformRoleItem | null>(null);
  const [deleting, setDeleting] = useState(false);

  // [COMMENT]: Sử dụng useQuery từ TanStack Query để quản lý danh sách roles
  const {
    data: roles = [],
    isLoading: loading,
    refetch: loadRoles,
  } = useQuery<PlatformRoleItem[]>({
    queryKey: [...scope, "rbac", "roles"],
    queryFn: () => listRoles(),
    enabled: workspaceReady,
  });

  // [COMMENT]: Mutation xóa Role và tự động cập nhật local query cache (Zero-Request UI update)
  const deleteRoleMutation = useMutation<void, Error, string>({
    mutationFn: (id) => deleteRole(id),
    onSuccess: (_, id) => {
      if (roleToDelete) {
        toast.success(`Role "${roleToDelete.name}" deleted successfully.`);
      }
      queryClient.setQueryData<PlatformRoleItem[]>([...scope, "rbac", "roles"], (prev) => {
        if (!prev) return [];
        return prev.filter((item) => item.id !== id);
      });
      setRoleToDelete(null);
    },
    onError: (err) => {
      console.error(err);
      toast.error(err instanceof Error ? err.message : "Failed to delete role.");
    },
    onSettled: () => {
      setDeleting(false);
    },
  });

  const handleDeleteRole = () => {
    if (!roleToDelete) return;
    setDeleting(true);
    deleteRoleMutation.mutate(roleToDelete.id);
  };

  // Apply filters on front-end list
  const filteredRoles = roles.filter((r) => {
    const matchesSearch = r.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
      r.code.toLowerCase().includes(searchQuery.toLowerCase());

    const matchesScope = scopeFilter === "all" ||
      (scopeFilter === "platform" && r.scope === "platform") ||
      (scopeFilter === "custom" && r.scope !== "platform");

    const matchesLevel = levelFilter === "all" || r.role_level.toString() === levelFilter;

    return matchesSearch && matchesScope && matchesLevel;
  });

  // Calculate statistics dynamically from actual database values
  const totalRoles = roles.length;
  const platformRoles = roles.filter((r) => r.scope === "platform").length;
  const customRoles = roles.filter((r) => r.scope !== "platform").length;
  const totalAssignments = roles.reduce((acc, r) => acc + (r.assignments_count || 0), 0);

  return (
    <div className="space-y-6">
      {/* ========================================== */}
      {/* 1. Header Area (Azure Style) */}
      {/* ========================================== */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 border-b border-slate-200 dark:border-slate-800 pb-4">
        <div>
          <span className="text-[10px] font-bold uppercase tracking-wider text-slate-400 dark:text-slate-500 select-none">
            Identity & Access
          </span>
          <h1 className="text-xl font-bold tracking-tight text-slate-900 dark:text-slate-100 flex items-center gap-2 mt-0.5">
            <Shield className="h-5 w-5 text-blue-500" />
            <span>RBAC</span>
          </h1>
          <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
            Manage system roles, authorization matrices and permission hierarchy.
          </p>
        </div>
      </div>

      {/* ========================================== */}
      {/* 2. Statistics Summary (Enterprise Stats Row) */}
      {/* ========================================== */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 border-b border-slate-200 dark:border-slate-800 pb-5">
        <div className="space-y-1">
          <span className="text-[10px] font-bold uppercase tracking-wider text-slate-400 dark:text-slate-500 select-none">Roles</span>
          <p className="text-2xl font-bold font-mono tracking-tight text-slate-950 dark:text-slate-50">{totalRoles}</p>
        </div>
        <div className="space-y-1 border-l border-slate-150 dark:border-slate-850 pl-4">
          <span className="text-[10px] font-bold uppercase tracking-wider text-slate-400 dark:text-slate-500 select-none">Platform Scope</span>
          <p className="text-2xl font-bold font-mono tracking-tight text-slate-950 dark:text-slate-50">{platformRoles}</p>
        </div>
        <div className="space-y-1 border-l border-slate-150 dark:border-slate-850 pl-4">
          <span className="text-[10px] font-bold uppercase tracking-wider text-slate-400 dark:text-slate-500 select-none">Custom Roles</span>
          <p className="text-2xl font-bold font-mono tracking-tight text-slate-950 dark:text-slate-50">{customRoles}</p>
        </div>
        <div className="space-y-1 border-l border-slate-150 dark:border-slate-850 pl-4">
          <span className="text-[10px] font-bold uppercase tracking-wider text-slate-400 dark:text-slate-500 select-none">Assignments</span>
          <p className="text-2xl font-bold font-mono tracking-tight text-slate-950 dark:text-slate-50">{totalAssignments}</p>
        </div>
      </div>

      {/* ========================================== */}
      {/* 3. Toolbar Section (Filters & Action triggers) */}
      {/* ========================================== */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 ">
        <div className="flex flex-wrap items-center gap-2">
          {/* Search Input */}
          <div className="relative min-w-[180px] sm:min-w-[220px]">
            <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-slate-400 pointer-events-none" />
            <input
              type="text"
              placeholder="Search role..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="h-8 w-full pl-8 pr-3 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 text-xs text-foreground placeholder-slate-400 focus:outline-none focus:border-blue-500 transition-colors"
            />
          </div>

          {/* Scope filter */}
          <select
            value={scopeFilter}
            onChange={(e) => setScopeFilter(e.target.value)}
            className="h-8 px-2.5 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 text-xs font-semibold text-slate-600 dark:text-slate-300 focus:outline-none cursor-pointer"
          >
            <option value="all">Scope: All</option>
            <option value="platform">Platform</option>
            <option value="custom">Custom/Tenant</option>
          </select>

          {/* Level filter */}
          <select
            value={levelFilter}
            onChange={(e) => setLevelFilter(e.target.value)}
            className="h-8 px-2.5 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 text-xs font-semibold text-slate-600 dark:text-slate-300 focus:outline-none cursor-pointer"
          >
            <option value="all">Level: All</option>
            <option value="0">Level 0</option>
            <option value="1">Level 1</option>
            <option value="2">Level 2</option>
          </select>
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={() => void loadRoles()}
            disabled={loading}
            className="flex items-center justify-center h-8 px-3 rounded-lg border border-slate-200 dark:border-slate-800 hover:border-slate-300 dark:hover:border-slate-700 bg-white dark:bg-slate-900 text-xs font-semibold text-slate-600 dark:text-slate-300 hover:text-slate-950 dark:hover:text-slate-100 transition-colors disabled:opacity-50 cursor-pointer"
          >
            <RefreshCw className={cn("h-3 w-3 mr-1.5", loading ? "animate-spin" : "")} />
            <span>Refresh</span>
          </button>

          {canCreate && (
            <>
              <div className="h-4 w-px bg-slate-250 dark:bg-slate-800 mx-0.5" />
              <Link
                href="/rbac/create"
                className="flex items-center justify-center h-8 px-3.5 rounded-lg bg-blue-600 hover:bg-blue-700 text-xs font-semibold text-white transition-colors cursor-pointer shadow-xs"
              >
                <Plus className="h-3.5 w-3.5 mr-1.5" />
                <span>New Role</span>
              </Link>
            </>
          )}
        </div>
      </div>

      {/* ========================================== */}
      {/* 4. Main Resource Table */}
      {/* ========================================== */}
      <div className="bg-white dark:bg-slate-900/40 border border-slate-200 dark:border-slate-800 rounded-xl overflow-hidden shadow-xs">
        {loading && roles.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 text-slate-500">
            <Loader2 className="h-7 w-7 animate-spin text-blue-500 mb-3" />
            <span className="text-xs font-semibold uppercase tracking-wider animate-pulse">Fetching roles...</span>
          </div>
        ) : filteredRoles.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 px-6 text-center text-slate-400 dark:text-slate-500 select-none">
            <ShieldAlert className="h-10 w-10 text-slate-300 dark:text-slate-700 mb-3" />
            <p className="text-sm font-semibold">No visible roles found</p>
            <p className="text-xs mt-1 max-w-xs text-slate-400 leading-normal">
              No roles match your search or filter configuration. Check inputs or refresh.
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left border-collapse">
              <thead>
                <tr className="border-b border-slate-200 dark:border-slate-800 bg-slate-55/60 dark:bg-slate-950/20 text-[10px] font-bold uppercase tracking-wider text-slate-400 dark:text-slate-500 select-none">
                  <th className="px-5 py-3.5">Role</th>
                  <th className="px-5 py-3.5">Code</th>
                  <th className="px-5 py-3.5">Level</th>
                  <th className="px-5 py-3.5">Scope</th>
                  <th className="px-5 py-3.5">Assignments</th>
                  <th className="px-5 py-3.5">Permissions</th>
                  <th className="px-5 py-3.5">Created By</th>
                  <th className="px-5 py-3.5">Created</th>
                  <th className="px-5 py-3.5">Updated</th>
                  <th className="px-5 py-3.5 text-right pr-6">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-150 dark:divide-slate-800/80 text-[12.5px]">
                {filteredRoles.map((r) => {
                  const meta = getRoleIcon(r.code);
                  const Icon = meta.icon;

                  return (
                    <tr
                      key={r.id}
                      className="group relative hover:bg-slate-50/50 dark:hover:bg-slate-800/5 transition-all"
                    >
                      {/* Name & Desc */}
                      <td className="px-5 py-3.5 relative before:absolute before:left-0 before:top-0 before:bottom-0 before:w-[3px] before:bg-blue-500 before:scale-y-0 group-hover:before:scale-y-100 before:transition-transform before:origin-center">
                        <div className="flex items-start gap-3 pl-1">
                          <div className={cn("h-7 w-7 flex items-center justify-center rounded-lg border border-slate-200/50 dark:border-slate-800/50 shrink-0 mt-0.5", meta.iconColor)}>
                            <Icon className="h-3.5 w-3.5" />
                          </div>
                          <div className="flex flex-col min-w-0">
                            <Link
                              href={`/rbac/${r.id}`}
                              className="font-semibold text-slate-850 dark:text-slate-200 hover:text-blue-500 transition-colors truncate"
                            >
                              {r.name}
                            </Link>
                            <span className="text-[10px] text-slate-450 dark:text-slate-500 truncate mt-0.5 max-w-[240px]">
                              {r.description || meta.fallbackDesc}
                            </span>
                          </div>
                        </div>
                      </td>

                      {/* Code */}
                      <td className="px-5 py-3.5">
                        <CopyBadge value={r.code} />
                      </td>

                      {/* Role Level Badge */}
                      <td className="px-5 py-3.5">
                        <span
                          style={{ backgroundColor: "#2b2112", color: "#F5B642" }}
                          className="inline-flex items-center px-1.5 py-0.5 rounded text-[9.5px] font-bold font-mono tracking-wider border border-[#F5B642]/10"
                        >
                          Level {r.role_level}
                        </span>
                      </td>

                      {/* Scope */}
                      <td className="px-5 py-3.5">
                        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-slate-100 dark:bg-slate-800/80 text-slate-600 dark:text-slate-400 border border-slate-200 dark:border-slate-800/60 capitalize">
                          {r.scope}
                        </span>
                      </td>

                      {/* Assignments */}
                      <td className="px-5 py-3.5 font-medium text-slate-600 dark:text-slate-350">
                        {r.assignments_count === 1 ? "1 assignment" : `${r.assignments_count} assignments`}
                      </td>

                      {/* Permissions */}
                      <td className="px-5 py-3.5 font-medium text-slate-600 dark:text-slate-350">
                        {r.permissions_count === 1 ? "1 perm" : `${r.permissions_count} perms`}
                      </td>

                      {/* Created By */}
                      <td className="px-5 py-3.5">
                        <span className="text-xs font-semibold text-slate-700 dark:text-slate-350">
                          {r.created_by_name || "—"}
                        </span>
                      </td>

                      {/* Created */}
                      <td className="px-5 py-3.5 text-slate-400 dark:text-slate-500">
                        {formatDate(r.created_at)}
                      </td>

                      {/* Updated */}
                      <td className="px-5 py-3.5 text-slate-400 dark:text-slate-500">
                        {formatDate(r.updated_at)}
                      </td>

                      {/* Actions */}
                      <td className="px-5 py-3.5 text-right pr-6">
                        {canDelete && (
                          <button
                            onClick={() => setRoleToDelete(r)}
                            className="text-slate-450 hover:text-red-500 dark:text-slate-500 dark:hover:text-red-400 transition-colors p-1.5 rounded hover:bg-slate-100 dark:hover:bg-slate-850 cursor-pointer"
                            title="Delete Role"
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Delete Confirmation Modal */}
      {roleToDelete && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-900/60 backdrop-blur-xs">
          <div className="w-full max-w-md bg-white dark:bg-slate-950 border border-slate-250 dark:border-slate-850 rounded-xl shadow-xl overflow-hidden p-6 space-y-4">
            <div className="flex items-center gap-3 text-red-500">
              <ShieldAlert className="h-6 w-6" />
              <h3 className="text-sm font-bold uppercase tracking-wider select-none">Delete Role</h3>
            </div>
            <p className="text-xs text-slate-600 dark:text-slate-450 leading-relaxed">
              Are you sure you want to delete the role <strong className="text-slate-800 dark:text-slate-200">{roleToDelete.name}</strong> (<code className="text-[10px] font-mono bg-slate-100 dark:bg-slate-900 px-1 py-0.5 rounded">{roleToDelete.code}</code>)? This action cannot be undone.
            </p>
            {roleToDelete.assignments_count > 0 && (
              <div className="p-3 rounded-lg bg-amber-500/10 text-amber-600 dark:text-amber-500 text-[11px] font-medium border border-amber-500/20">
                Warning: This role is currently assigned to {roleToDelete.assignments_count} users. You must unassign them before deleting.
              </div>
            )}
            <div className="flex items-center justify-end gap-2 pt-2">
              <button
                onClick={() => setRoleToDelete(null)}
                disabled={deleting}
                className="h-8 px-3.5 rounded-lg border border-slate-200 dark:border-slate-800 hover:border-slate-300 dark:hover:border-slate-700 bg-white dark:bg-slate-900 text-xs font-semibold text-slate-600 dark:text-slate-350 transition-colors disabled:opacity-50 cursor-pointer"
              >
                Cancel
              </button>
              <button
                onClick={handleDeleteRole}
                disabled={deleting || roleToDelete.assignments_count > 0}
                className="h-8 px-3.5 rounded-lg bg-red-600 hover:bg-red-700 text-xs font-semibold text-white transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer shadow-xs"
              >
                {deleting ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
