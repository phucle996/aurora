"use client";

import React, { useState, useCallback, useMemo } from "react";
import { useRouter } from "next/navigation";
import { HardDrive, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { listBuckets, type BucketItem } from "@/lib/api/storage";
import { useUserSession } from "@/hooks/useUserSession";
import { useWorkspace } from "@/context/WorkspaceContext";
import RouteGuard from "@/components/route-guard";
import { Button } from "@/components/ui/button";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import { BucketFilters } from "./components/BucketFilters";
import { BucketTable } from "./components/BucketTable";
import { useBucketSizesSync } from "@/hooks/useBucketSizesSync";

function StorageDirectoryContent() {
  const router = useRouter();
  const { checkPermission, profile } = useUserSession();
  const { activeWorkspaceID, loading: wsLoading } = useWorkspace();

  const queryClient = useQueryClient();
  // [COMMENT]: Đăng ký lắng nghe sự kiện đồng bộ dung lượng từ Centrifugo WebSocket.
  // Cập nhật trực tiếp dữ liệu vào cache của React Query để tránh flicker UI và tối ưu tải mạng.
  useBucketSizesSync(
    profile?.user_id,
    useCallback((updatedSizes: Record<string, number>) => {
      queryClient.setQueryData<BucketItem[]>(
        ["buckets", activeWorkspaceID],
        (prevBuckets) => {
          if (!prevBuckets) return [];
          return prevBuckets.map((bucket) => {
            if (updatedSizes[bucket.Name] !== undefined) {
              return {
                ...bucket,
                UsedBytes: updatedSizes[bucket.Name],
              };
            }
            return bucket;
          });
        }
      );
      toast.success("Storage bucket capacities synced in real-time", {
        id: "realtime-bucket-sizes-sync",
      });
    }, [queryClient, activeWorkspaceID])
  );


  // Filter states
  const [searchTerm, setSearchTerm] = useState("");
  const [statusFilter, setStatusFilter] = useState("All");

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
    queryKey: ["buckets", activeWorkspaceID],
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
      const matchSearch = b.Name.toLowerCase().includes(searchTerm.toLowerCase());
      const matchStatus = statusFilter === "All" || b.Status === statusFilter;
      return matchSearch && matchStatus;
    });

    return [...res].sort((a, b) => {
      let va: any = "";
      let vb: any = "";

      if (sortKey === "name") {
        va = a.Name.toLowerCase();
        vb = b.Name.toLowerCase();
      } else if (sortKey === "status") {
        va = a.Status.toLowerCase();
        vb = b.Status.toLowerCase();
      } else if (sortKey === "quota") {
        va = a.CapacityQuotaBytes;
        vb = b.CapacityQuotaBytes;
      } else if (sortKey === "created_at") {
        va = new Date(a.CreatedAt).getTime();
        vb = new Date(b.CreatedAt).getTime();
      }

      if (va < vb) return sortDir === "asc" ? -1 : 1;
      if (va > vb) return sortDir === "asc" ? 1 : -1;
      return 0;
    });
  }, [buckets, searchTerm, statusFilter, sortKey, sortDir]);

  // Pagination calculation
  const totalBuckets = filteredBuckets.length;
  const totalPages = Math.ceil(totalBuckets / bucketsPerPage);
  const paginatedBuckets = useMemo(() => {
    const startIndex = (currentPage - 1) * bucketsPerPage;
    return filteredBuckets.slice(startIndex, startIndex + bucketsPerPage);
  }, [filteredBuckets, currentPage]);

  const handleClearFilters = () => {
    setSearchTerm("");
    setStatusFilter("All");
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
          statusFilter={statusFilter}
          setStatusFilter={setStatusFilter}
          handleClearFilters={handleClearFilters}
          setCurrentPage={setCurrentPage}
          onCreateClick={() => router.push("/storage/create")}
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

export default function StorageDirectoryPage() {
  return (
    <RouteGuard requiredKey="storage:bucket" requiredAction="read">
      <StorageDirectoryContent />
    </RouteGuard>
  );
}
