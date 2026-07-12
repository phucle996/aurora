"use client";

import React, { useEffect, useState, useMemo } from "react";
import { useRouter, useParams } from "next/navigation";
import Link from "next/link";
import {
  Shield,
  ArrowLeft,
  Loader2,
  CheckSquare,
  ChevronDown,
  ChevronRight,
  Search,
  Copy,
  Check,
  Calendar,
  Edit
} from "lucide-react";
import { toast } from "sonner";
import { getRoleDetails, type PermissionItem } from "@/lib/api/rbac";
import RouteGuard from "@/components/route-guard";
import { useQuery } from "@tanstack/react-query";

// [COMMENT]: Định dạng ngày tháng hiển thị thông minh
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

// [COMMENT]: Component CopyBadge sao chép code vai trò chỉ đọc
function CopyBadge({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  const handleCopy = (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <button
      onClick={handleCopy}
      type="button"
      className="inline-flex items-center gap-1.5 px-2 py-0.5 rounded bg-slate-50 hover:bg-slate-100 dark:bg-slate-900/40 dark:hover:bg-slate-800 text-slate-655 dark:text-slate-400 border border-slate-200/60 dark:border-slate-800/80 text-[10px] font-mono transition-colors group select-all cursor-pointer"
    >
      <span>{value}</span>
      {copied ? (
        <Check className="h-3 w-3 text-emerald-500" />
      ) : (
        <Copy className="h-3 w-3 text-slate-400 group-hover:text-slate-600 dark:group-hover:text-slate-355 transition-colors" />
      )}
    </button>
  );
}

function ViewRoleContent() {
  const router = useRouter();
  const { id } = useParams() as { id: string };

  const [searchQuery, setSearchQuery] = useState("");
  const [expandedModules, setExpandedModules] = useState<Record<string, boolean>>({});
  const [expandedObjects, setExpandedObjects] = useState<Record<string, boolean>>({});

  // [COMMENT]: Sử dụng useQuery từ TanStack Query để quản lý chi tiết Role
  const {
    data: roleData = null,
    isLoading: loading,
  } = useQuery({
    queryKey: ["role", id],
    queryFn: async () => {
      try {
        const data = await getRoleDetails(id);
        return data;
      } catch (err: any) {
        toast.error(err.message || "Failed to load role details.");
        router.push("/rbac");
        return null;
      }
    },
    enabled: !!id,
  });

  // Mặc định expand tất cả các Module có trong quyền được gán sau khi roleData được load
  useEffect(() => {
    if (roleData?.permissions) {
      const initialModules: Record<string, boolean> = {};
      roleData.permissions.forEach((p) => {
        if (p.module) {
          initialModules[p.module] = true;
        }
      });
      setExpandedModules(initialModules);
    }
  }, [roleData]);

  const name = roleData?.name || "";
  const code = roleData?.code || "";
  const description = roleData?.description || "";
  const roleLevel = roleData?.role_level ?? 8;
  const scope = roleData?.scope || "platform";
  const assignmentsCount = roleData?.assignments_count || 0;
  const permissionsCount = roleData?.permissions_count || 0;
  const createdAt = roleData?.created_at || "";
  const updatedAt = roleData?.updated_at || "";
  const grantedTree = roleData?.permissions || [];

  // [COMMENT]: Thực hiện bộ lọc tìm kiếm và dựng cây 3 bậc từ danh sách phẳng
  const filteredTree = useMemo(() => {
    const root: Record<string, {
      name: string;
      objects: Record<string, {
        name: string;
        behaviors: { id: string; behavior: string; description: string; fullName: string }[];
      }>;
    }> = {};

    grantedTree.forEach((perm) => {
      const fullName = `${perm.module}:${perm.object}:${perm.behavior}`;

      if (searchQuery.trim()) {
        const query = searchQuery.toLowerCase();
        const matchesQuery =
          fullName.toLowerCase().includes(query) ||
          perm.description?.toLowerCase().includes(query);
        if (!matchesQuery) return;
      }

      const mod = perm.module || "other";
      const obj = perm.object || "default";

      if (!root[mod]) {
        root[mod] = { name: mod, objects: {} };
      }
      if (!root[mod].objects[obj]) {
        root[mod].objects[obj] = { name: obj, behaviors: [] };
      }
      root[mod].objects[obj].behaviors.push({
        id: perm.id,
        behavior: perm.behavior,
        description: perm.description || "",
        fullName,
      });
    });

    // Trả về định dạng ModuleNode[] cho giao diện JSX map
    return Object.values(root).map((mod) => ({
      name: mod.name,
      objects: Object.values(mod.objects),
    }));
  }, [grantedTree, searchQuery]);

  if (loading) {
    return (
      <div className="flex flex-col items-center justify-center py-40 text-slate-500">
        <Loader2 className="h-8 w-8 animate-spin text-blue-500 mb-3" />
        <span className="text-xs font-semibold uppercase tracking-wider animate-pulse">Loading details...</span>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header Area */}
      <div className="flex items-center justify-between border-b border-slate-200 dark:border-slate-800 pb-5">
        <div className="flex items-center gap-3">
          <button
            type="button"
            onClick={() => router.push("/rbac")}
            className="flex items-center justify-center h-8 w-8 rounded-lg border border-slate-200 dark:border-slate-800 hover:bg-slate-55 dark:hover:bg-slate-800 text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 transition-colors cursor-pointer"
          >
            <ArrowLeft className="h-4 w-4" />
          </button>
          <div>
            <h1 className="text-xl font-bold tracking-tight text-slate-900 dark:text-slate-100 flex items-center gap-2">
              <Shield className="h-5 w-5 text-blue-500" />
              <span>Role Authorization Profile</span>
            </h1>
            <p className="mt-1 text-xs font-semibold text-slate-505 dark:text-slate-400">
              View security privileges, scope, and level configurations.
            </p>
          </div>
        </div>

        {/* Edit Navigation Button */}
        <div>
          <Link
            href={`/rbac/${id}/edit`}
            className="flex items-center justify-center h-8 px-4 rounded-lg bg-blue-600 hover:bg-blue-700 text-xs font-semibold text-white transition-colors cursor-pointer shadow-xs gap-1.5"
          >
            <Edit className="h-3.5 w-3.5" />
            <span>Edit Role</span>
          </Link>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">
        {/* ======================================================== */}
        {/* CỘT TRÁI (2/3 chiều rộng = 8/12): Role Details & Granted Permissions */}
        {/* ======================================================== */}
        <div className="lg:col-span-8 space-y-6">
          {/* Info Card */}
          <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-6 shadow-xs space-y-4">
            <h3 className="text-xs font-bold uppercase tracking-wider text-slate-855 dark:text-slate-200 border-b border-slate-150 dark:border-slate-800 pb-2">
              Role Details
            </h3>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <label className="text-xs font-bold uppercase tracking-wider text-slate-505 dark:text-slate-455">
                  Role Name
                </label>
                <p className="text-sm font-semibold text-slate-800 dark:text-slate-200 h-9 flex items-center pl-1 border border-transparent select-all">
                  {name}
                </p>
              </div>

              <div className="space-y-1.5">
                <label className="text-xs font-bold uppercase tracking-wider text-slate-505 dark:text-slate-455 block mb-1">
                  Role Code / Key
                </label>
                <div className="h-9 flex items-center">
                  <CopyBadge value={code} />
                </div>
              </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="space-y-1.5">
                <label className="text-xs font-bold uppercase tracking-wider text-slate-505 dark:text-slate-455">
                  Role Hierarchy Level
                </label>
                <div className="h-9 flex items-center">
                  <span
                    style={{ backgroundColor: "#2b2112", color: "#F5B642" }}
                    className="inline-flex items-center px-2.5 py-0.5 rounded text-[10px] font-bold font-mono tracking-wider border border-[#F5B642]/10 select-all"
                  >
                    Level {roleLevel}
                  </span>
                </div>
              </div>

              <div className="flex items-end pb-1.5">
                <span className="text-[10px] text-slate-405 dark:text-slate-600 font-semibold leading-tight">
                  Lower levels indicate higher privileges. Level 0 is Root, 1 is Admin. Scope, Code Key, and Hierarchy Level are immutable.
                </span>
              </div>
            </div>

            <div className="space-y-1.5">
              <label className="text-xs font-bold uppercase tracking-wider text-slate-505 dark:text-slate-455">
                Description
              </label>
              <p className="text-sm text-slate-650 dark:text-slate-350 border border-slate-200/40 dark:border-slate-800/40 bg-slate-50/30 dark:bg-slate-950/10 rounded-lg p-3 min-h-[50px] leading-relaxed select-all">
                {description || "No description provided for this role."}
              </p>
            </div>
          </div>

          {/* Permissions Catalog Tree */}
          <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-6 shadow-xs space-y-4">
            <div className="border-b border-slate-150 dark:border-slate-800 pb-3">
              <h3 className="text-xs font-bold uppercase tracking-wider text-slate-855 dark:text-slate-200">
                Granted Permissions Matrix
              </h3>
            </div>

            {/* Filter */}
            <div className="relative">
              <Search className="absolute left-2.5 top-2.5 h-3.5 w-3.5 text-slate-400 pointer-events-none" />
              <input
                type="text"
                placeholder="Filter granted permissions (e.g. compute:vps)..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="h-8 w-full pl-8 pr-3 rounded-lg border border-slate-200 dark:border-slate-800 bg-transparent text-xs text-foreground placeholder-slate-455 focus:outline-hidden focus:border-blue-500 transition-colors"
              />
            </div>

            {/* Permissions list */}
            {grantedTree.length === 0 ? (
              <div className="text-center py-10 text-slate-455 dark:text-slate-505 font-semibold text-xs leading-normal">
                No permissions granted to this role.
              </div>
            ) : filteredTree.length === 0 ? (
              <div className="text-center py-10 text-slate-400 dark:text-slate-600 text-xs font-semibold select-none">
                No permissions match your filter.
              </div>
            ) : (
              <div className="space-y-4 select-none">
                {filteredTree.map((modNode) => {
                  const isModExpanded = !!expandedModules[modNode.name];

                  const totalBehaviors: string[] = [];
                  modNode.objects.forEach((o) => {
                    o.behaviors.forEach((b) => totalBehaviors.push(b.id));
                  });

                  return (
                    <div
                      key={modNode.name}
                      className="border border-slate-200 dark:border-slate-800/80 rounded-xl p-3.5 bg-slate-50/20 dark:bg-slate-955/5 space-y-3"
                    >
                      {/* Module Header */}
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

                          <div className="flex items-center text-blue-500">
                            <CheckSquare className="h-4 w-4" />
                          </div>

                          <span className="text-xs font-bold text-slate-900 dark:text-slate-100 uppercase tracking-wider font-mono">
                            {modNode.name}
                          </span>
                        </div>

                        <span className="text-[10px] text-slate-400 dark:text-slate-505 font-bold">
                          {totalBehaviors.length} permissions
                        </span>
                      </div>

                      {/* Objects (Level 2) */}
                      {isModExpanded && (
                        <div className="space-y-4 pt-1">
                          {modNode.objects.map((objNode) => {
                            const uniqueKey = `${modNode.name}:${objNode.name}`;
                            const isObjExpanded = !!expandedObjects[uniqueKey];

                            return (
                              <div
                                key={objNode.name}
                                className="ml-1.5 border-l-2 border-slate-200 dark:border-slate-800 pl-3.5 space-y-2"
                              >
                                {/* Object Header */}
                                <div className="flex items-center gap-1.5 py-0.5">
                                  <button
                                    type="button"
                                    onClick={() =>
                                      setExpandedObjects((prev) => ({ ...prev, [uniqueKey]: !prev[uniqueKey] }))
                                    }
                                    className="p-0.5 hover:bg-slate-200 dark:hover:bg-slate-850 rounded text-slate-400 hover:text-slate-655 transition-colors cursor-pointer"
                                  >
                                    {isObjExpanded ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                                  </button>

                                  <div className="flex items-center text-blue-500">
                                    <CheckSquare className="h-3.5 w-3.5" />
                                  </div>

                                  <span className="text-xs font-bold text-slate-700 dark:text-slate-300 font-mono">
                                    {objNode.name}
                                  </span>
                                  <span className="text-[10px] text-slate-400 dark:text-slate-505 font-semibold">
                                    ({objNode.behaviors.length})
                                  </span>
                                </div>

                                {/* Behaviors (Level 3) */}
                                {isObjExpanded && (
                                  <div className="ml-5 grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-2 pt-1 pb-1.5">
                                    {objNode.behaviors.map((b) => (
                                      <div
                                        key={b.id}
                                        className="flex items-start gap-2 p-2 rounded-lg border bg-blue-500/10 border-blue-500/20 text-blue-900 dark:text-blue-200 select-none group"
                                      >
                                        <div className="mt-0.5 text-blue-500">
                                          <CheckSquare className="h-3.5 w-3.5" />
                                        </div>
                                        <div className="space-y-0.5 leading-none">
                                          <span className="text-[11px] font-bold font-mono text-slate-800 dark:text-slate-200">
                                            {b.behavior}
                                          </span>
                                          {b.description && (
                                            <p className="text-[9px] text-slate-400 dark:text-slate-550 font-medium leading-normal">
                                              {b.description}
                                            </p>
                                          )}
                                        </div>
                                      </div>
                                    ))}
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
        </div>

        {/* ======================================================== */}
        {/* CỘT PHẢI (1/3 chiều rộng = 4/12): Audit Status */}
        {/* ======================================================== */}
        <div className="lg:col-span-4 lg:sticky lg:top-6 space-y-6">
          {/* Audit Card */}
          <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl p-5 shadow-xs space-y-4">
            <div className="border-b border-slate-200 dark:border-slate-800 pb-3 flex items-center justify-between">
              <h3 className="text-xs font-bold uppercase tracking-wider text-slate-855 dark:text-slate-200">
                Role Audit Status
              </h3>
              <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-slate-100 dark:bg-slate-800/80 text-slate-650 dark:text-slate-400 border border-slate-200 dark:border-slate-800/60 capitalize">
                {scope} Scope
              </span>
            </div>

            <div className="space-y-3.5 text-xs text-slate-600 dark:text-slate-400">
              <div className="flex items-center justify-between">
                <span>Current Assignments</span>
                <strong className="text-slate-850 dark:text-slate-200 font-mono">
                  {assignmentsCount === 1 ? "1 user" : `${assignmentsCount} users`}
                </strong>
              </div>

              <div className="flex items-center justify-between">
                <span>Permissions Granted</span>
                <strong className="text-slate-850 dark:text-slate-200 font-mono">
                  {permissionsCount} rules
                </strong>
              </div>

              {createdAt && (
                <div className="flex items-center justify-between">
                  <span className="flex items-center gap-1">
                    <Calendar className="h-3 w-3 text-slate-455" />
                    <span>Created At</span>
                  </span>
                  <strong className="text-slate-850 dark:text-slate-200 font-mono text-[10.5px]">
                    {formatDate(createdAt)}
                  </strong>
                </div>
              )}

              {updatedAt && (
                <div className="flex items-center justify-between border-t border-slate-105 dark:border-slate-800/60 pt-3">
                  <span className="flex items-center gap-1">
                    <Calendar className="h-3 w-3 text-slate-455" />
                    <span>Last Updated</span>
                  </span>
                  <strong className="text-slate-850 dark:text-slate-200 font-mono text-[10.5px]">
                    {formatDate(updatedAt)}
                  </strong>
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

export default function ViewRolePage() {
  return (
    <RouteGuard requiredKey="iam:role" requiredAction="read">
      <ViewRoleContent />
    </RouteGuard>
  );
}
