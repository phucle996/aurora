import React from "react";
import { Loader2, ShieldAlert, CheckCircle2, XCircle, ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, ArrowUpDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import type { PlatformUserItem } from "@/features/iam/users-api";

export type ExtendedUser = PlatformUserItem & {
  ext: {
    fullname: string;
    mfaEnabled: boolean;
    devicesCount: number;
    lastActiveStr: string;
    ip: string;
    displayRole: string;
  };
};

function SortIcon({ col, sortKey, sortDir }: { col: string; sortKey: string; sortDir: "asc" | "desc" }) {
  return (
    <ArrowUpDown
      className={cn(
        "h-3 w-3 ml-1 shrink-0 transition-colors",
        sortKey === col ? "text-foreground" : "text-muted-foreground/45",
        sortKey === col && sortDir === "desc" && "rotate-180"
      )}
    />
  );
}

interface UserTableProps {
  loading: boolean;
  users: PlatformUserItem[];
  filteredUsers: ExtendedUser[];
  paginatedUsers: ExtendedUser[];
  selectedUserId: string | null;
  setSelectedUserId: (id: string | null) => void;
  currentPage: number;
  setCurrentPage: React.Dispatch<React.SetStateAction<number>>;
  usersPerPage: number;
  totalUsers: number;
  totalPages: number;
  getAvatarColors: (name: string) => string;
  sortKey: string;
  sortDir: "asc" | "desc";
  onSort: (key: string) => void;
}

export function UserTable({
  loading,
  users,
  filteredUsers,
  paginatedUsers,
  selectedUserId,
  setSelectedUserId,
  currentPage,
  setCurrentPage,
  usersPerPage,
  totalUsers,
  totalPages,
  getAvatarColors,
  sortKey,
  sortDir,
  onSort,
}: UserTableProps) {
  return (
    <>
      {loading && users.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-24 text-muted-foreground">
          <Loader2 className="h-8 w-8 animate-spin text-blue-500 mb-3" />
          <span className="text-xs font-semibold uppercase tracking-wider animate-pulse">Syncing User Directory...</span>
        </div>
      ) : filteredUsers.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-20 px-6 text-center text-muted-foreground select-none">
          <ShieldAlert className="h-12 w-12 text-muted-foreground/60 mb-3" />
          <p className="text-sm font-semibold">No matching users found</p>
          <p className="text-xs mt-1.5 max-w-xs text-muted-foreground leading-normal">
            Adjust your search keywords or active filters to inspect matching accounts.
          </p>
        </div>
      ) : (
        <div className="overflow-x-auto select-none rounded-xl border border-border">
          <table className="w-full text-left border-collapse table-auto">
            <thead>
              <tr className="border-b border-border bg-muted/20 text-[10px] font-extrabold uppercase tracking-wider text-muted-foreground select-none">
                <th className="px-6 py-3.5 cursor-pointer hover:text-foreground transition-colors" onClick={() => onSort("username")}>
                  <span className="inline-flex items-center">User <SortIcon col="username" sortKey={sortKey} sortDir={sortDir} /></span>
                </th>
                <th className="px-6 py-3.5 cursor-pointer hover:text-foreground transition-colors" onClick={() => onSort("status")}>
                  <span className="inline-flex items-center">Status <SortIcon col="status" sortKey={sortKey} sortDir={sortDir} /></span>
                </th>
                <th className="px-6 py-3.5 cursor-pointer hover:text-foreground transition-colors" onClick={() => onSort("role")}>
                  <span className="inline-flex items-center">Roles <SortIcon col="role" sortKey={sortKey} sortDir={sortDir} /></span>
                </th>
                <th className="px-6 py-3.5 cursor-pointer hover:text-foreground transition-colors" onClick={() => onSort("mfa")}>
                  <span className="inline-flex items-center">MFA <SortIcon col="mfa" sortKey={sortKey} sortDir={sortDir} /></span>
                </th>
                <th className="px-6 py-3.5 cursor-pointer hover:text-foreground transition-colors" onClick={() => onSort("devices")}>
                  <span className="inline-flex items-center">Devices <SortIcon col="devices" sortKey={sortKey} sortDir={sortDir} /></span>
                </th>
                <th className="px-6 py-3.5 cursor-pointer hover:text-foreground transition-colors" onClick={() => onSort("last_active")}>
                  <span className="inline-flex items-center">Last Active <SortIcon col="last_active" sortKey={sortKey} sortDir={sortDir} /></span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border text-[13px]">
              {paginatedUsers.map((u) => {
                const initials = u.ext.fullname
                  .split(" ")
                  .map((n: string) => n.charAt(0))
                  .slice(0, 2)
                  .join("")
                  .toUpperCase() || "U";

                return (
                  <tr
                    key={u.id}
                    onClick={() => {
                      setSelectedUserId(selectedUserId === u.id ? null : u.id);
                    }}
                    className={cn(
                      "hover:bg-muted/40 transition-colors cursor-pointer select-none",
                      selectedUserId === u.id && "bg-muted/80"
                    )}
                  >
                    {/* USER details */}
                    <td className="px-6 py-3.5">
                      <div className="flex items-center gap-3">
                        {/* Avatar */}
                        <div className={cn(
                          "h-9 w-9 flex items-center justify-center rounded-full text-xs font-bold border",
                          getAvatarColors(u.id)
                        )}>
                          {initials}
                        </div>
                        {/* Text info */}
                        <div className="flex flex-col">
                          <span className="font-bold text-foreground">
                            {u.ext.fullname}
                          </span>
                          <span className="text-[11px] text-muted-foreground mt-0.5">
                            {u.email}
                          </span>
                        </div>
                      </div>
                    </td>

                    {/* STATUS indicator */}
                    <td className="px-6 py-3.5">
                      <span className="inline-flex items-center gap-2 text-xs font-bold select-none">
                        <span className={cn(
                          "h-1.5 w-1.5 rounded-full",
                          u.status === "active"
                            ? "bg-emerald-500 animate-pulse"
                            : u.status === "pending-active"
                              ? "bg-amber-500"
                              : "bg-red-505"
                        )} />
                        <span className={cn(
                          "capitalize",
                          u.status === "active"
                            ? "text-emerald-600 dark:text-emerald-400"
                            : u.status === "pending-active"
                              ? "text-amber-605 dark:text-amber-400"
                              : "text-red-650 dark:text-red-400"
                        )}>
                          {u.status === "pending-active" ? "Pending" : u.status}
                        </span>
                      </span>
                    </td>

                    {/* ROLE badge */}
                    <td className="px-6 py-3.5">
                      <Badge variant="outline" className={cn(
                        "inline-flex items-center px-2 py-0.5 rounded-md text-[10px] font-extrabold tracking-wider h-5 border",
                        getAvatarColors(u.id)
                      )}>
                        {u.ext.displayRole}
                      </Badge>
                    </td>

                    {/* MFA status indicator */}
                    <td className="px-6 py-3.5">
                      {u.ext.mfaEnabled ? (
                        <span className="inline-flex items-center gap-1.5 text-[11px] font-bold text-emerald-600 dark:text-emerald-450 select-none">
                          <CheckCircle2 className="h-3.5 w-3.5" />
                          <span>Enabled</span>
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1.5 text-[11px] font-bold text-muted-foreground select-none">
                          <XCircle className="h-3.5 w-3.5" />
                          <span>Disabled</span>
                        </span>
                      )}
                    </td>

                    {/* DEVICES details */}
                    <td className="px-6 py-3.5 font-bold text-foreground">
                      {u.ext.devicesCount}
                    </td>

                    {/* LAST ACTIVE log */}
                    <td className="px-6 py-3.5">
                      <div className="flex flex-col">
                        <span className="font-semibold text-foreground">
                          {u.ext.lastActiveStr}
                        </span>
                        <span className="text-[10px] text-muted-foreground font-mono mt-0.5">
                          {u.ext.ip}
                        </span>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Footer Pagination */}
      {filteredUsers.length > 0 && (
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 border-t border-border p-4 bg-muted/20 text-xs font-semibold text-muted-foreground">
          <div>
            Showing {Math.min((currentPage - 1) * usersPerPage + 1, totalUsers)} to {Math.min(currentPage * usersPerPage, totalUsers)} of {totalUsers} users
          </div>
          <div className="flex items-center gap-4">
            <div className="flex items-center gap-1">
              <Button
                variant="outline"
                size="icon"
                onClick={() => setCurrentPage(1)}
                disabled={currentPage === 1}
                className="h-7 w-7 border-border text-foreground hover:bg-muted cursor-pointer transition-colors"
              >
                <ChevronsLeft className="h-3.5 w-3.5" />
              </Button>
              <Button
                variant="outline"
                size="icon"
                onClick={() => setCurrentPage((p) => Math.max(p - 1, 1))}
                disabled={currentPage === 1}
                className="h-7 w-7 border-border text-foreground hover:bg-muted cursor-pointer transition-colors"
              >
                <ChevronLeft className="h-3.5 w-3.5" />
              </Button>
              {Array.from({ length: totalPages }, (_, i) => i + 1)
                .filter((p) => Math.abs(p - currentPage) <= 1 || p === 1 || p === totalPages)
                .map((p, idx, arr) => {
                  const showDots = idx > 0 && p - arr[idx - 1] > 1;
                  return (
                    <React.Fragment key={p}>
                      {showDots && <span className="px-1 text-muted-foreground">...</span>}
                      <Button
                        variant={currentPage === p ? "default" : "outline"}
                        size="icon"
                        onClick={() => setCurrentPage(p)}
                        className={cn(
                          "h-7 w-7 text-xs font-bold cursor-pointer transition-colors",
                          currentPage !== p && "border-border text-foreground hover:bg-muted"
                        )}
                      >
                        {p}
                      </Button>
                    </React.Fragment>
                  );
                })}
              <Button
                variant="outline"
                size="icon"
                onClick={() => setCurrentPage((p) => Math.min(p + 1, totalPages))}
                disabled={currentPage === totalPages}
                className="h-7 w-7 border-border text-foreground hover:bg-muted cursor-pointer transition-colors"
              >
                <ChevronRight className="h-3.5 w-3.5" />
              </Button>
              <Button
                variant="outline"
                size="icon"
                onClick={() => setCurrentPage(totalPages)}
                disabled={currentPage === totalPages}
                className="h-7 w-7 border-border text-foreground hover:bg-muted cursor-pointer transition-colors"
              >
                <ChevronsRight className="h-3.5 w-3.5" />
              </Button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
