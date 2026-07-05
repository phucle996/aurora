"use client";

import React, { useEffect, useState, useCallback } from "react";
import { Users, Trash2, RefreshCw, ShieldAlert, Loader2, ArrowLeft } from "lucide-react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { listAdminUsers, deleteAdminUser, type AdminUserItem } from "@/lib/api/session";
import { useUserSession } from "@/hooks/useUserSession";
import { cn } from "@/lib/utils";

// [COMMENT]: Trang Admin User Directory hiển thị danh sách người dùng hệ thống và cho phép thao tác quản trị
export default function UserDirectoryPage() {
  const router = useRouter();
  const { checkPermission } = useUserSession();
  const [users, setUsers] = useState<AdminUserItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  // [COMMENT]: Kiểm tra quyền hạn xoá người dùng động bằng checkPermission
  const canDeleteUser = React.useMemo(() => {
    return checkPermission("*:*:iam:users", "delete");
  }, [checkPermission]);

  // [COMMENT]: Tải danh sách user từ Backend
  const loadUsers = useCallback(async (showToast = false) => {
    setLoading(true);
    try {
      const data = await listAdminUsers();
      setUsers(data);
      if (showToast) {
        toast.success("User directory synchronized.");
      }
    } catch (err) {
      console.error(err);
      toast.error(err instanceof Error ? err.message : "Failed to load users list.");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadUsers();
  }, [loadUsers]);

  // [COMMENT]: Xử lý xóa vĩnh viễn user có đối chiếu check quyền
  const handleDelete = async (id: string, username: string) => {
    if (!confirm(`Are you absolutely sure you want to permanently delete user "${username}"? All associated workspaces, keys, and settings will be permanently destroyed. This action CANNOT be undone.`)) {
      return;
    }

    setDeletingId(id);
    try {
      await deleteAdminUser(id);
      toast.success(`User "${username}" successfully deleted.`);
      // Nạp lại danh sách sau khi xóa thành công
      await loadUsers();
    } catch (err) {
      console.error(err);
      toast.error(err instanceof Error ? err.message : "Failed to delete user.");
    } finally {
      setDeletingId(null);
    }
  };

  return (
    <div className="space-y-6">
      {/* ========================================== */}
      {/* 1. Header Area with dynamic actions */}
      {/* ========================================== */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 border-b border-slate-200 dark:border-slate-800 pb-5">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-slate-900 dark:text-slate-100 flex items-center gap-2">
            <Users className="h-5 w-5 text-blue-500" />
            <span>User Directory</span>
          </h1>
          <p className="mt-1.5 text-xs font-semibold text-slate-500 dark:text-slate-400">
            Manage infrastructure users, authorization scopes and security levels.
          </p>
        </div>

        <div className="flex items-center gap-2">
          <button
            onClick={() => void loadUsers(true)}
            disabled={loading}
            className="flex items-center justify-center h-8 px-3 rounded-lg border border-slate-200 dark:border-slate-800 hover:border-slate-300 dark:hover:border-slate-700 bg-white dark:bg-slate-900 text-xs font-semibold text-slate-600 dark:text-slate-300 hover:text-slate-950 dark:hover:text-slate-100 transition-colors disabled:opacity-50 cursor-pointer"
          >
            <RefreshCw className={cn("h-3.5 w-3.5 mr-1.5", loading ? "animate-spin" : "")} />
            <span>Sync</span>
          </button>
        </div>
      </div>

      {/* ========================================== */}
      {/* 2. Main Content Card */}
      {/* ========================================== */}
      <div className="bg-white dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-xl overflow-hidden shadow-xs">
        {loading && users.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-20 text-slate-500">
            <Loader2 className="h-7 w-7 animate-spin text-blue-500 mb-3" />
            <span className="text-xs font-semibold uppercase tracking-wider animate-pulse">Fetching directory...</span>
          </div>
        ) : users.length === 0 ? (
          <div className="flex flex-col items-center justify-center py-16 px-6 text-center text-slate-400 dark:text-slate-500 select-none">
            <ShieldAlert className="h-10 w-10 text-slate-300 dark:text-slate-700 mb-3" />
            <p className="text-sm font-semibold">No visible users found</p>
            <p className="text-xs mt-1 max-w-xs text-slate-400 leading-normal">
              You either do not have clearance to view higher-privileged levels or the directory index is empty.
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left border-collapse">
              <thead>
                <tr className="border-b border-slate-200 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-950/20 text-[10px] font-bold uppercase tracking-wider text-slate-500 dark:text-slate-400 select-none">
                  <th className="px-6 py-3.5">Username</th>
                  <th className="px-6 py-3.5">Email Address</th>
                  <th className="px-6 py-3.5">Status</th>
                  <th className="px-6 py-3.5">Created At</th>
                  <th className="px-6 py-3.5 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-150 dark:divide-slate-800 text-[13px]">
                {users.map((u) => {
                  const isCurrentDeleting = deletingId === u.id;

                  return (
                    <tr
                      key={u.id}
                      className="hover:bg-slate-50/40 dark:hover:bg-slate-800/10 transition-colors"
                    >
                      {/* Username */}
                      <td className="px-6 py-4 font-bold text-slate-800 dark:text-slate-200">
                        {u.username}
                      </td>

                      {/* Email */}
                      <td className="px-6 py-4 text-slate-600 dark:text-slate-400">
                        {u.email}
                      </td>

                      {/* Status Badge */}
                      <td className="px-6 py-4">
                        <span
                          className={cn(
                            "inline-flex items-center px-2 py-0.5 rounded-md text-[10px] font-bold uppercase tracking-wider",
                            u.status === "active"
                              ? "bg-emerald-500/10 text-emerald-500 border border-emerald-500/15"
                              : u.status === "pending-active"
                              ? "bg-amber-500/10 text-amber-500 border border-amber-500/15"
                              : "bg-slate-500/10 text-slate-500 border border-slate-500/15"
                          )}
                        >
                          {u.status}
                        </span>
                      </td>

                      {/* Created At */}
                      <td className="px-6 py-4 text-slate-500 dark:text-slate-500 font-medium">
                        {new Date(u.created_at).toLocaleString()}
                      </td>

                      {/* Action buttons */}
                      <td className="px-6 py-4 text-right">
                        {canDeleteUser ? (
                          <button
                            onClick={() => handleDelete(u.id, u.username)}
                            disabled={isCurrentDeleting}
                            className="inline-flex h-7 w-7 items-center justify-center rounded-lg border border-slate-200 dark:border-slate-800 hover:border-red-200 dark:hover:border-red-900/30 hover:bg-red-50 dark:hover:bg-red-950/20 text-slate-400 hover:text-red-500 dark:hover:text-red-400 transition-all cursor-pointer disabled:opacity-50"
                            title="Delete User permanently"
                          >
                            {isCurrentDeleting ? (
                              <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                              <Trash2 className="h-3.5 w-3.5" />
                            )}
                          </button>
                        ) : (
                          <span className="text-[10px] text-slate-400 dark:text-slate-600 italic select-none">
                            Read Only
                          </span>
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
    </div>
  );
}
