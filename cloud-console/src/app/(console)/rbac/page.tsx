"use client";

import React, { useEffect, useState, useCallback } from "react";
import Link from "next/link";
import {
  Shield,
  RefreshCw,
  Key,
  ShieldAlert,
  Loader2,
  Plus,
  Copy,
  Check,
  Search,
  Settings,
  User,
  MoreVertical
} from "lucide-react";
import { toast } from "sonner";
import { listRoles, type PlatformRoleItem } from "@/lib/api/rbac";
import { cn } from "@/lib/utils";
import RouteGuard from "@/components/route-guard";
import { useUserSession } from "@/hooks/useUserSession";

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
function AccessControlContent() {
  const [roles, setRoles] = useState<PlatformRoleItem[]>([]);
  const [loading, setLoading] = useState(true);
  const { checkPermission } = useUserSession();

  // Local Filtering states
  const [searchQuery, setSearchQuery] = useState("");
  const [scopeFilter, setScopeFilter] = useState("all");
  const [levelFilter, setLevelFilter] = useState("all");

  const canCreate = checkPermission("iam:role", "write");

  // [COMMENT]: Gọi API lấy danh sách Platform Roles
  const loadRoles = useCallback(async (showToast = false) => {
    setLoading(true);
    try {
      const data = await listRoles();
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
            onClick={() => void loadRoles(true)}
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
                      <td className="px-5 py-3.5">
                        <div className="flex items-start gap-3">
                          <div className={cn("h-7 w-7 flex items-center justify-center rounded-lg border border-slate-200/50 dark:border-slate-800/50 shrink-0 mt-0.5", meta.iconColor)}>
                            <Icon className="h-3.5 w-3.5" />
                          </div>
                          <div className="flex flex-col min-w-0">
                            <span className="font-semibold text-slate-800 dark:text-slate-200 truncate">
                              {r.name}
                            </span>
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
                          L{r.role_level}
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
                        <button className="text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 transition-colors p-1 rounded hover:bg-slate-100 dark:hover:bg-slate-850 cursor-pointer">
                          <MoreVertical className="h-3.5 w-3.5" />
                        </button>
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
