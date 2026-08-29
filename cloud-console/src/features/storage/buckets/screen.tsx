"use client";

import React, { useState, useMemo } from "react";
import { useRouter } from "next/navigation";
import { HardDrive, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { listBuckets, type BucketItem } from "@/features/storage/api";
import { useUserSession } from "@/session/use-session";
import { useWorkspace } from "@/context/WorkspaceContext";
import { Button } from "@/components/ui/button";
import { useQuery } from "@tanstack/react-query";
import { useConsoleQueryScope } from "@/shared/query/scope";

import { BucketFilters } from "./BucketFilters";
import { BucketTable } from "./BucketTable";

export function StorageDirectoryScreen() {
  const router = useRouter();
  const { checkPermission } = useUserSession();
  const { activeWorkspaceID, loading: wsLoading } = useWorkspace();
  const scope = useConsoleQueryScope();

  // Filter states
  const [searchTerm, setSearchTerm] = useState("");

  // Pagination states
  const [currentPage, setCurrentPage] = useState(1);
  const bucketsPerPage = 10;

  // Sorting states
  const [sortKey, setSortKey] = useState<string>("name");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");

  // [COMMENT]: Sử dụng hook useQuery từ TanStack Query để quản lý danh sách storage buckets.
  // Cơ chế này tự động xử lý caching, deduplication request, và tự động đồng bộ ngầm.
  const {
    data: buckets = [],
    isLoading: loading, // Map state loading cũ sang isLoading để giữ tương thích với UI
    isRefetching,
    refetch,
  } = useQuery<BucketItem[]>({
    queryKey: [...scope, "storage", "buckets"],
    queryFn: () => listBuckets(),
    enabled: !!activeWorkspaceID && !wsLoading,
  });

  // Sorting toggle
  const handleSort = (key: string) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("asc");
    }
  };

  // Filter and Sort logic
  const filteredBuckets = useMemo(() => {
    const res = buckets.filter((b) => {
      // [COMMENT]: Lọc dữ liệu sử dụng thuộc tính 'name' dạng snake_case/lowercase
      const matchSearch = b.name.toLowerCase().includes(searchTerm.toLowerCase());
      return matchSearch;
    });

    return [...res].sort((a, b) => {
      let va: string | number = "";
      let vb: string | number = "";

      // [COMMENT]: Sắp xếp dựa theo các thuộc tính đã đổi thành snake_case/lowercase
      if (sortKey === "name") {
        va = a.name.toLowerCase();
        vb = b.name.toLowerCase();
      } else if (sortKey === "quota") {
        va = a.capacity_quota_bytes;
        vb = b.capacity_quota_bytes;
      } else if (sortKey === "created_at") {
        va = new Date(a.created_at).getTime();
        vb = new Date(b.created_at).getTime();
      }

      if (va < vb) return sortDir === "asc" ? -1 : 1;
      if (va > vb) return sortDir === "asc" ? 1 : -1;
      return 0;
    });
  }, [buckets, searchTerm, sortKey, sortDir]);

  // Pagination calculation
  const totalBuckets = filteredBuckets.length;
  const totalPages = Math.ceil(totalBuckets / bucketsPerPage);
  const paginatedBuckets = useMemo(() => {
    const startIndex = (currentPage - 1) * bucketsPerPage;
    return filteredBuckets.slice(startIndex, startIndex + bucketsPerPage);
  }, [filteredBuckets, currentPage]);

  const handleClearFilters = () => {
    setSearchTerm("");
    setCurrentPage(1);
    toast.success("Storage bucket filter terms cleared");
  };

  const canCreate = useMemo(() => {
    return checkPermission("storage:bucket", "write");
  }, [checkPermission]);

  return (
    <div className="flex flex-col gap-6 w-full relative pb-10 text-foreground min-h-[calc(100vh-110px)] items-stretch">
      {/* 1. Header Section */}
      <div className="flex flex-col xl:flex-row xl:items-center xl:justify-between gap-4 border-b border-border pb-5">
        <div className="flex items-start gap-3">
          <div className="h-10 w-10 flex items-center justify-center rounded-xl bg-blue-600/10 border border-blue-500/20 text-blue-500">
            <HardDrive className="h-5 w-5" />
          </div>
          <div className="flex flex-col">
            <h1 className="text-xl font-black text-foreground select-none tracking-tight">
              Object Storage
            </h1>
            <p className="text-xs text-muted-foreground font-medium mt-1 max-w-xl leading-relaxed select-none">
              Manage physical MinIO and S3-compatible cloud storage buckets, adjust capacity limit quotas, track real-time health statuses, and generate secure client credentials.
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button
            onClick={() => void refetch()}
            disabled={loading || isRefetching || wsLoading}
            variant="outline"
            size="sm"
            className="font-bold cursor-pointer transition-colors"
          >
            <RefreshCw className={loading || isRefetching ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} />
            <span>Sync</span>
          </Button>
        </div>
      </div>

      {/* 2. Filters & List Table */}
      <div className="flex flex-col gap-4">
        <BucketFilters
          searchTerm={searchTerm}
          setSearchTerm={setSearchTerm}
          handleClearFilters={handleClearFilters}
          setCurrentPage={setCurrentPage}
          onCreateClick={() => router.push("/storage/new")}
          canCreate={canCreate}
        />

        <BucketTable
          loading={loading}
          buckets={buckets}
          filteredBuckets={filteredBuckets}
          paginatedBuckets={paginatedBuckets}
          currentPage={currentPage}
          setCurrentPage={setCurrentPage}
          bucketsPerPage={bucketsPerPage}
          totalBuckets={totalBuckets}
          totalPages={totalPages}
          sortKey={sortKey}
          sortDir={sortDir}
          onSort={handleSort}
        />
      </div>
    </div>
  );
}
