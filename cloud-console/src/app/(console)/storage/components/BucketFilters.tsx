import React from "react";
import { Search, RotateCcw, X, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";

interface BucketFiltersProps {
  searchTerm: string;
  setSearchTerm: (val: string) => void;
  handleClearFilters: () => void;
  setCurrentPage: (page: number) => void;
  onCreateClick: () => void;
  canCreate: boolean;
}

export function BucketFilters({
  searchTerm,
  setSearchTerm,
  handleClearFilters,
  setCurrentPage,
  onCreateClick,
  canCreate,
}: BucketFiltersProps) {
  const hasActiveFilters = searchTerm !== "";

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 text-xs w-full">
      <div className="flex flex-wrap items-center gap-2 flex-1">
        {/* Search Box */}
        <div className="relative w-64">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground/60 pointer-events-none" />
          <input
            type="text"
            placeholder="Search buckets by name..."
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

      <div>
        {canCreate && (
          <Button
            onClick={onCreateClick}
            className="h-8 bg-blue-600 hover:bg-blue-700 text-white rounded-lg px-3 flex items-center gap-1 font-bold cursor-pointer transition-all active:scale-[0.98] shadow-sm select-none"
          >
            <Plus className="h-3.5 w-3.5" />
            <span>Create Bucket</span>
          </Button>
        )}
      </div>
    </div>
  );
}
