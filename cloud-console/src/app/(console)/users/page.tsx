"use client";

import React, { useEffect, useState, useCallback, useMemo } from "react";
import { Users, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { listUsers, updateUserStatus, type PlatformUserItem } from "@/lib/api/user";
import { useUserSession } from "@/hooks/useUserSession";
import { cn } from "@/lib/utils";
import RouteGuard from "@/components/route-guard";
import { UserDetailPanel } from "./UserDetailPanel";
import { Button } from "@/components/ui/button";
import { UserFilters } from "./UserFilters";
import { UserTable } from "./UserTable";

const getExtendedUserData = (u: PlatformUserItem) => {
  const mfaEnabled = u.mfa_enabled ?? false;
  const devicesCount = u.devices_count ?? 0;

  // [COMMENT]: Hiển thị thời điểm hoạt động gần nhất từ device thực tế
  const lastSeenAt = u.last_seen_at ? new Date(u.last_seen_at) : null;
  let lastActiveStr = "Never";
  if (lastSeenAt) {
    const diffMs = Date.now() - lastSeenAt.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    if (diffMins < 60) lastActiveStr = `${diffMins}m ago`;
    else if (diffMins < 1440) lastActiveStr = `${Math.floor(diffMins / 60)}h ago`;
    else lastActiveStr = `${Math.floor(diffMins / 1440)}d ago`;
  }

  const ip = u.last_seen_ip || "—";
  const displayRole = u.role || "Platform Member";

  return {
    fullname: u.fullname || u.username,
    mfaEnabled,
    devicesCount,
    lastActiveStr,
    ip,
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

  const [users, setUsers] = useState<PlatformUserItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [searchTerm, setSearchTerm] = useState("");
  const [statusFilter, setStatusFilter] = useState("All");
  const [roleFilter, setRoleFilter] = useState("All");
  const [mfaFilter, setMfaFilter] = useState("All");

  const [currentPage, setCurrentPage] = useState(1);
  const [updatingId, setUpdatingId] = useState<string | null>(null);
  const [selectedUserId, setSelectedUserId] = useState<string | null>(null);
  const [activeDetailTab, setActiveDetailTab] = useState("Overview");

  // [COMMENT]: State quản lý việc sắp xếp cột của bảng Users
  const [sortKey, setSortKey] = useState<string>("username");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");

  const usersPerPage = 10;

  const loadUsers = useCallback(async (isRefresh = false) => {
    try {
      if (isRefresh) {
        setLoading(true);
      }
      const data = await listUsers();
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
      await updateUserStatus(id, status);
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
      ext: getExtendedUserData(u),
    }));
  }, [users]);

  // [COMMENT]: Toggle sắp xếp khi click tiêu đề cột
  const handleSort = (key: string) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("asc");
    }
  };

  const filteredUsers = useMemo(() => {
    const res = extendedUsers.filter((u) => {
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

      return matchSearch && matchStatus && matchRole && matchMfa;
    });

    return [...res].sort((a, b) => {
      let va: any = "";
      let vb: any = "";

      if (sortKey === "username") {
        va = a.ext.fullname.toLowerCase();
        vb = b.ext.fullname.toLowerCase();
      } else if (sortKey === "status") {
        va = a.status.toLowerCase();
        vb = b.status.toLowerCase();
      } else if (sortKey === "role") {
        va = (a.ext.displayRole || "").toLowerCase();
        vb = (b.ext.displayRole || "").toLowerCase();
      } else if (sortKey === "mfa") {
        va = a.ext.mfaEnabled ? 1 : 0;
        vb = b.ext.mfaEnabled ? 1 : 0;
      } else if (sortKey === "devices") {
        va = a.ext.devicesCount;
        vb = b.ext.devicesCount;
      } else if (sortKey === "last_active") {
        va = a.last_seen_at ? new Date(a.last_seen_at).getTime() : 0;
        vb = b.last_seen_at ? new Date(b.last_seen_at).getTime() : 0;
      }

      if (va < vb) return sortDir === "asc" ? -1 : 1;
      if (va > vb) return sortDir === "asc" ? 1 : -1;
      return 0;
    });
  }, [extendedUsers, searchTerm, statusFilter, roleFilter, mfaFilter, sortKey, sortDir]);

  const selectedUser = useMemo(() => {
    if (!selectedUserId) return null;
    return extendedUsers.find((u) => u.id === selectedUserId) || null;
  }, [extendedUsers, selectedUserId]);

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
    const handleOutsideClick = () => { };
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

        {/* 2. Flat Toolbar & User Filters */}
        <div className="flex flex-col gap-4">
          <UserFilters
            searchTerm={searchTerm}
            setSearchTerm={setSearchTerm}
            statusFilter={statusFilter}
            setStatusFilter={setStatusFilter}
            roleFilter={roleFilter}
            setRoleFilter={setRoleFilter}
            mfaFilter={mfaFilter}
            setMfaFilter={setMfaFilter}
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
            sortKey={sortKey}
            sortDir={sortDir}
            onSort={handleSort}
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
