import React from "react";
import { Search, RotateCcw } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";

interface UserFiltersProps {
  searchTerm: string;
  setSearchTerm: (val: string) => void;
  statusFilter: string;
  setStatusFilter: (val: string) => void;
  roleFilter: string;
  setRoleFilter: (val: string) => void;
  mfaFilter: string;
  setMfaFilter: (val: string) => void;
  riskFilter: string;
  setRiskFilter: (val: string) => void;
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
  riskFilter,
  setRiskFilter,
  uniqueRoles,
  handleClearFilters,
  setCurrentPage,
}: UserFiltersProps) {
  return (
    <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-3 p-4 bg-card text-card-foreground w-full">
      <div className="flex flex-wrap items-center gap-3 flex-1">
        {/* Search Box */}
        <div className="relative flex-1 min-w-[240px]">
          <Search className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input
            type="text"
            placeholder="Search users by name, email, or username..."
            value={searchTerm}
            onChange={(e) => {
              setSearchTerm(e.target.value);
              setCurrentPage(1);
            }}
            className="pl-9 bg-card border-border text-foreground placeholder:text-muted-foreground focus-visible:border-blue-600 focus-visible:ring-blue-600/15"
          />
        </div>

        {/* Status Dropdown */}
        <select
          value={statusFilter}
          onChange={(e) => {
            setStatusFilter(e.target.value);
            setCurrentPage(1);
          }}
          className="h-9 px-2.5 rounded-lg bg-card border border-border text-xs text-foreground font-medium focus:outline-hidden cursor-pointer hover:bg-muted/40 transition-colors"
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
          className="h-9 px-2.5 rounded-lg bg-card border border-border text-xs text-foreground font-medium focus:outline-hidden cursor-pointer hover:bg-muted/40 transition-colors"
        >
          <option value="All">Role: All</option>
          {uniqueRoles.map((role) => (
            <option key={role} value={role}>Role: {role}</option>
          ))}
        </select>

        {/* MFA Dropdown */}
        <select
          value={mfaFilter}
          onChange={(e) => {
            setMfaFilter(e.target.value);
            setCurrentPage(1);
          }}
          className="h-9 px-2.5 rounded-lg bg-card border border-border text-xs text-foreground font-medium focus:outline-hidden cursor-pointer hover:bg-muted/40 transition-colors"
        >
          <option value="All">MFA: All</option>
          <option value="Enabled">MFA: Enabled</option>
          <option value="Disabled">MFA: Disabled</option>
        </select>

        {/* Risk Dropdown */}
        <select
          value={riskFilter}
          onChange={(e) => {
            setRiskFilter(e.target.value);
            setCurrentPage(1);
          }}
          className="h-9 px-2.5 rounded-lg bg-card border border-border text-xs text-foreground font-medium focus:outline-hidden cursor-pointer hover:bg-muted/40 transition-colors"
        >
          <option value="All">Risk: All</option>
          <option value="Low">Risk: Low</option>
          <option value="Medium">Risk: Medium</option>
          <option value="High">Risk: High</option>
        </select>
      </div>

      {/* Clear and filter icons */}
      <div className="flex items-center gap-2 border-t lg:border-t-0 border-border pt-3 lg:pt-0">
        <Button
          variant="ghost"
          onClick={handleClearFilters}
          className="text-xs font-bold text-blue-600 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300 hover:bg-blue-500/5 dark:hover:bg-blue-400/5 cursor-pointer transition-all flex items-center gap-1"
        >
          <RotateCcw className="h-3 w-3" />
          <span>Clear</span>
        </Button>
      </div>
    </div>
  );
}
