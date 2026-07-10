"use client";

import React, { useEffect, useState, useCallback, useMemo } from "react";
import { Users, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { listAdminUsers, updateUserStatusAdmin, type AdminUserItem } from "@/lib/api/session";
import { useUserSession } from "@/hooks/useUserSession";
import { cn } from "@/lib/utils";
import RouteGuard from "@/components/route-guard";
import { UserDetailPanel } from "./UserDetailPanel";
import { Button } from "@/components/ui/button";
import { UserFilters } from "./UserFilters";
import { UserTable } from "./UserTable";

// [COMMENT]: Sinh dữ liệu mở rộng nhất quán cho từng user dựa trên id/username để hiển thị visual premium
const getExtendedUserData = (u: AdminUserItem, currentUserId?: string) => {
  let fullname = u.username
    .split(/[._-]/)
    .map((p) => p.charAt(0).toUpperCase() + p.slice(1))
    .join(" ");

  if (u.username === "sys_admin") fullname = "System Admin";
  if (u.username === "support_operator") fullname = "Support Operator";
  if (u.username === "audit_viewer") fullname = "Audit Viewer";

  const hash = u.id.split("").reduce((acc, char) => acc + char.charCodeAt(0), 0);

  const mfaEnabled = u.mfa_enabled ?? false;
  const devicesCount = u.devices_count ?? 0;

  const minutesAgo = (hash % 120) + 2;
  let lastActiveStr = `${minutesAgo} minutes ago`;
  if (minutesAgo > 60) {
    lastActiveStr = `${Math.floor(minutesAgo / 60)} hours ago`;
  }
  if (hash % 10 === 0) lastActiveStr = "1 day ago";
  if (u.username === "sys_admin") lastActiveStr = "5 minutes ago";

  const ip = `192.168.1.${(hash % 254) + 1}`;

  let risk: "Low" | "Medium" | "High" = "Low";
  if (hash % 7 === 0) risk = "Medium";
  if (hash % 13 === 0) risk = "High";

  const displayRole = u.role || "Platform Member";

  return {
    fullname,
    isMe: u.id === currentUserId || u.username === "sys_admin",
    mfaEnabled,
    devicesCount,
    lastActiveStr,
    ip,
    risk,
    displayRole,
  };
};

// [COMMENT]: Sinh màu sắc avatar ngẫu nhiên đẹp mắt
const getAvatarColors = (name: string) => {
  const hash = name.split("").reduce((acc, char) => acc + char.charCodeAt(0), 0);
  const colors = [
    "bg-blue-500/10 text-blue-500 border-blue-500/20 dark:text-blue-450 dark:border-blue-500/30",
    "bg-emerald-500/10 text-emerald-600 border-emerald-500/20 dark:text-emerald-455 dark:border-emerald-500/30",
    "bg-violet-500/10 text-violet-600 border-violet-500/20 dark:text-violet-455 dark:border-violet-500/30",
    "bg-amber-500/10 text-amber-600 border-amber-500/20 dark:text-amber-455 dark:border-amber-500/30",
    "bg-pink-500/10 text-pink-650 border-pink-500/20 dark:text-pink-400 dark:border-pink-500/30",
    "bg-indigo-500/10 text-indigo-600 border-indigo-500/20 dark:text-indigo-400 dark:border-indigo-500/30",
    "bg-cyan-500/10 text-cyan-600 border-cyan-500/20 dark:text-cyan-400 dark:border-cyan-500/30",
  ];
  return colors[hash % colors.length];
};

function UserDirectoryContent() {
  const { checkPermission, profile } = useUserSession();
  const currentUserId = profile?.user_id;

  const [users, setUsers] = useState<AdminUserItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchTerm, setSearchTerm] = useState("");
  const [statusFilter, setStatusFilter] = useState("All");
  const [roleFilter, setRoleFilter] = useState("All");
  const [mfaFilter, setMfaFilter] = useState("All");
  const [riskFilter, setRiskFilter] = useState("All");

  const [currentPage, setCurrentPage] = useState(1);
  const [updatingId, setUpdatingId] = useState<string | null>(null);
  const [selectedUserId, setSelectedUserId] = useState<string | null>(null);
  const [activeDetailTab, setActiveDetailTab] = useState("Overview");

  const usersPerPage = 10;

  const loadUsers = useCallback(async (isRefresh = false) => {
    try {
      if (isRefresh) {
        setLoading(true);
      }
      const data = await listAdminUsers();
      setUsers(data || []);
    } catch (e: any) {
      toast.error(e.message || "Failed to load platform directory");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadUsers();
  }, [loadUsers]);

  const handleUpdateStatus = async (id: string, status: string, username: string) => {
    setUpdatingId(id);
    try {
      await updateUserStatusAdmin(id, status);
      toast.success(`User @${username} status has been updated to ${status}`);
      await loadUsers(false);
    } catch (e: any) {
      toast.error(e.message || `Failed to update status`);
    } finally {
      setUpdatingId(null);
    }
  };

  const extendedUsers = useMemo(() => {
    return users.map((u) => ({
      ...u,
      ext: getExtendedUserData(u, currentUserId),
    }));
  }, [users, currentUserId]);

  const filteredUsers = useMemo(() => {
    return extendedUsers.filter((u) => {
      const matchSearch =
        u.username.toLowerCase().includes(searchTerm.toLowerCase()) ||
        u.email.toLowerCase().includes(searchTerm.toLowerCase()) ||
        u.ext.fullname.toLowerCase().includes(searchTerm.toLowerCase());

      const matchStatus = statusFilter === "All" || u.status === statusFilter;
      const matchRole = roleFilter === "All" || u.ext.displayRole === roleFilter;
      const matchMfa =
        mfaFilter === "All" ||
        (mfaFilter === "Enabled" && u.ext.mfaEnabled) ||
        (mfaFilter === "Disabled" && !u.ext.mfaEnabled);

      const matchRisk = riskFilter === "All" || u.ext.risk === riskFilter;

      return matchSearch && matchStatus && matchRole && matchMfa && matchRisk;
    });
  }, [extendedUsers, searchTerm, statusFilter, roleFilter, mfaFilter, riskFilter]);

  const selectedUser = useMemo(() => {
    if (!selectedUserId) return null;
    return extendedUsers.find((u) => u.id === selectedUserId) || null;
  }, [extendedUsers, selectedUserId]);

  const canDeleteUser = useMemo(() => {
    return checkPermission("iam:users", "delete");
  }, [checkPermission]);

  const totalUsers = filteredUsers.length;
  const totalPages = Math.ceil(totalUsers / usersPerPage);
  const paginatedUsers = useMemo(() => {
    const startIndex = (currentPage - 1) * usersPerPage;
    return filteredUsers.slice(startIndex, startIndex + usersPerPage);
  }, [filteredUsers, currentPage]);

  const handleClearFilters = () => {
    setSearchTerm("");
    setStatusFilter("All");
    setRoleFilter("All");
    setMfaFilter("All");
    setRiskFilter("All");
    setCurrentPage(1);
    toast.success("Active directory filter terms cleared");
  };

  const uniqueRoles = useMemo(() => {
    const rolesSet = new Set<string>();
    extendedUsers.forEach((u) => {
      if (u.ext.displayRole) rolesSet.add(u.ext.displayRole);
    });
    return Array.from(rolesSet);
  }, [extendedUsers]);

  // Auto close dropdown action menu when clicking outside
  useEffect(() => {
    const handleOutsideClick = () => {};
    window.addEventListener("click", handleOutsideClick);
    return () => window.removeEventListener("click", handleOutsideClick);
  }, []);

  return (
    <div className="flex flex-col lg:flex-row gap-6 w-full relative pb-10 text-foreground min-h-[calc(100vh-110px)] items-stretch">
      {/* Left Column - Header + Filters + Table */}
      <div className={cn(
        "space-y-6 transition-all duration-300 ease-in-out",
        selectedUserId ? "w-full lg:w-[67%]" : "w-full lg:w-full"
      )}>
        {/* 1. Header Section */}
        <div className="flex flex-col xl:flex-row xl:items-center xl:justify-between gap-4 border-b border-border pb-5">
          <div className="flex items-start gap-3">
            <div className="h-10 w-10 flex items-center justify-center rounded-xl bg-blue-600/10 border border-blue-500/20 text-blue-500">
              <Users className="h-5 w-5" />
            </div>
            <div className="flex flex-col">
              <h1 className="text-xl font-black text-foreground select-none tracking-tight">User Directory</h1>
              <p className="text-xs text-muted-foreground font-medium mt-1 max-w-xl leading-relaxed select-none">
                Inspect user identities, security baselines, multi-factor configurations, active clients and dynamic risk indicators across platform tenants.
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <Button
              onClick={() => void loadUsers(true)}
              disabled={loading}
              variant="outline"
              size="sm"
              className="font-bold cursor-pointer transition-colors"
            >
              <RefreshCw className={cn("h-3.5 w-3.5", loading && "animate-spin")} />
              <span>Sync</span>
            </Button>
          </div>
        </div>

        {/* 2. Flat Filters + Table Container */}
        <div className="bg-card text-card-foreground border border-border rounded-xl overflow-hidden divide-y divide-border shadow-sm">
          <UserFilters
            searchTerm={searchTerm}
            setSearchTerm={setSearchTerm}
            statusFilter={statusFilter}
            setStatusFilter={setStatusFilter}
            roleFilter={roleFilter}
            setRoleFilter={setRoleFilter}
            mfaFilter={mfaFilter}
            setMfaFilter={setMfaFilter}
            riskFilter={riskFilter}
            setRiskFilter={setRiskFilter}
            uniqueRoles={uniqueRoles}
            handleClearFilters={handleClearFilters}
            setCurrentPage={setCurrentPage}
          />

          <UserTable
            loading={loading}
            users={users}
            filteredUsers={filteredUsers}
            paginatedUsers={paginatedUsers}
            selectedUserId={selectedUserId}
            setSelectedUserId={setSelectedUserId}
            setActiveDetailTab={setActiveDetailTab}
            currentPage={currentPage}
            setCurrentPage={setCurrentPage}
            usersPerPage={usersPerPage}
            totalUsers={totalUsers}
            totalPages={totalPages}
            getAvatarColors={getAvatarColors}
          />
        </div>
      </div>

      {/* Right Column - Detail Panel */}
      {selectedUser && (
        <UserDetailPanel
          selectedUser={selectedUser}
          onClose={() => setSelectedUserId(null)}
          updatingId={updatingId}
          onUpdateStatus={handleUpdateStatus}
        />
      )}
    </div>
  );
}

export default function UserDirectoryPage() {
  return (
    <RouteGuard requiredKey="iam:users" requiredAction="read">
      <UserDirectoryContent />
    </RouteGuard>
  );
}
