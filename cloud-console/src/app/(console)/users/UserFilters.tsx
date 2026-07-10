import React from "react";
import { Search, RotateCcw, X } from "lucide-react";

interface UserFiltersProps {
  searchTerm: string;
  setSearchTerm: (val: string) => void;
  statusFilter: string;
  setStatusFilter: (val: string) => void;
  roleFilter: string;
  setRoleFilter: (val: string) => void;
  mfaFilter: string;
  setMfaFilter: (val: string) => void;
  uniqueRoles: string[];
  handleClearFilters: () => void;
  setCurrentPage: (page: number) => void;
}

export function UserFilters({
  searchTerm,
  setSearchTerm,
  statusFilter,
  setStatusFilter,
  roleFilter,
  setRoleFilter,
  mfaFilter,
  setMfaFilter,
  uniqueRoles,
  handleClearFilters,
  setCurrentPage,
}: UserFiltersProps) {
  const hasActiveFilters =
    searchTerm !== "" ||
    statusFilter !== "All" ||
    roleFilter !== "All" ||
    mfaFilter !== "All";

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 text-xs w-full">
      <div className="flex flex-wrap items-center gap-2 flex-1">
        {/* Search Box */}
        <div className="relative w-64">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground/60 pointer-events-none" />
          <input
            type="text"
            placeholder="Search users..."
            value={searchTerm}
            onChange={(e) => {
              setSearchTerm(e.target.value);
              setCurrentPage(1);
            }}
            className="w-full h-8 pl-8 pr-7 text-xs bg-background border border-border rounded-lg focus:outline-none focus:border-blue-500 text-foreground placeholder:text-muted-foreground/50 transition-colors"
          />
          {searchTerm && (
            <button
              onClick={() => {
                setSearchTerm("");
                setCurrentPage(1);
              }}
              className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground/60 hover:text-muted-foreground cursor-pointer"
            >
              <X className="h-3 w-3" />
            </button>
          )}
        </div>

        {/* Status Dropdown */}
        <select
          value={statusFilter}
          onChange={(e) => {
            setStatusFilter(e.target.value);
            setCurrentPage(1);
          }}
          className="h-8 px-2.5 rounded-lg bg-background border border-border text-xs text-foreground font-medium focus:outline-none cursor-pointer hover:bg-muted/40 transition-colors"
        >
          <option value="All">Status: All</option>
          <option value="active">Status: Active</option>
          <option value="pending-active">Status: Pending</option>
          <option value="suspended">Status: Suspended</option>
          <option value="disabled">Status: Disabled</option>
        </select>

        {/* Role Dropdown */}
        <select
          value={roleFilter}
          onChange={(e) => {
            setRoleFilter(e.target.value);
            setCurrentPage(1);
          }}
          className="h-8 px-2.5 rounded-lg bg-background border border-border text-xs text-foreground font-medium focus:outline-none cursor-pointer hover:bg-muted/40 transition-colors"
        >
          <option value="All">Role: All</option>
          {uniqueRoles.map((role) => (
            <option key={role} value={role}>
              Role: {role}
            </option>
          ))}
        </select>

        {/* MFA Dropdown */}
        <select
          value={mfaFilter}
          onChange={(e) => {
            setMfaFilter(e.target.value);
            setCurrentPage(1);
          }}
          className="h-8 px-2.5 rounded-lg bg-background border border-border text-xs text-foreground font-medium focus:outline-none cursor-pointer hover:bg-muted/40 transition-colors"
        >
          <option value="All">MFA: All</option>
          <option value="Enabled">MFA: Enabled</option>
          <option value="Disabled">MFA: Disabled</option>
        </select>

        {/* Clear Button */}
        {hasActiveFilters && (
          <button
            onClick={handleClearFilters}
            className="flex items-center gap-1 h-8 px-2.5 rounded-lg border border-transparent hover:border-border text-xs font-semibold text-blue-600 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300 hover:bg-muted/55 cursor-pointer transition-all"
          >
            <RotateCcw className="h-3 w-3" />
            <span>Clear Filters</span>
          </button>
        )}
      </div>
    </div>
  );
}
