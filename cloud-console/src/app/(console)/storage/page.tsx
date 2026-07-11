"use client";

import React, { useEffect, useState, useCallback, useMemo } from "react";
import { HardDrive, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { listBuckets, type BucketItem } from "@/lib/api/storage";
import { fetchZoneCatalog, type ZoneCatalogItem } from "@/lib/api/zone";
import { useUserSession } from "@/hooks/useUserSession";
import { useWorkspace } from "@/context/WorkspaceContext";
import RouteGuard from "@/components/route-guard";
import { Button } from "@/components/ui/button";

import { BucketFilters } from "./components/BucketFilters";
import { BucketTable } from "./components/BucketTable";
import { CreateBucketModal } from "./components/CreateBucketModal";

function StorageDirectoryContent() {
  const { checkPermission } = useUserSession();
  const { activeWorkspaceID, loading: wsLoading } = useWorkspace();

  const [buckets, setBuckets] = useState<BucketItem[]>([]);
  const [zones, setZones] = useState<ZoneCatalogItem[]>([]);
  const [loading, setLoading] = useState(true);

  // Filter states
  const [searchTerm, setSearchTerm] = useState("");
  const [zoneFilter, setZoneFilter] = useState("All");
  const [statusFilter, setStatusFilter] = useState("All");

  // Pagination states
  const [currentPage, setCurrentPage] = useState(1);
  const bucketsPerPage = 10;

  // Sorting states
  const [sortKey, setSortKey] = useState<string>("name");
  const [sortDir, setSortDir] = useState<"asc" | "desc">("asc");

  // Create Modal state
  const [isCreateOpen, setIsCreateOpen] = useState(false);

  // Load zones catalog
  useEffect(() => {
    let active = true;
    async function loadZones() {
      try {
        const zoneData = await fetchZoneCatalog();
        if (active) {
          setZones(zoneData || []);
        }
      } catch (err: any) {
        console.error("Failed to load zone catalog:", err);
      }
    }
    void loadZones();
    return () => {
      active = false;
    };
  }, []);

  // Map Zone ID -> Zone Name for table lookup
  const zoneMap = useMemo(() => {
    const map: Record<string, string> = {};
    zones.forEach((z) => {
      map[z.id] = z.name;
    });
    return map;
  }, [zones]);

  const loadBuckets = useCallback(async (isRefresh = false) => {
    if (!activeWorkspaceID) {
      setBuckets([]);
      setLoading(false);
      return;
    }
    try {
      if (isRefresh) {
        setLoading(true);
      }
      const data = await listBuckets();
      setBuckets(data || []);
    } catch (e: any) {
      toast.error(e.message || "Failed to load object storage buckets");
    } finally {
      setLoading(false);
    }
  }, [activeWorkspaceID]);

  // Load buckets on mount / workspace change
  useEffect(() => {
    void loadBuckets(true);
  }, [loadBuckets]);

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
      const matchZone = zoneFilter === "All" || b.ZoneID === zoneFilter;
      const matchStatus = statusFilter === "All" || b.Status === statusFilter;
      return matchSearch && matchZone && matchStatus;
    });

    return [...res].sort((a, b) => {
      let va: any = "";
      let vb: any = "";

      if (sortKey === "name") {
        va = a.Name.toLowerCase();
        vb = b.Name.toLowerCase();
      } else if (sortKey === "zone") {
        va = (zoneMap[a.ZoneID] || "").toLowerCase();
        vb = (zoneMap[b.ZoneID] || "").toLowerCase();
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
  }, [buckets, searchTerm, zoneFilter, statusFilter, sortKey, sortDir, zoneMap]);

  // Pagination calculation
  const totalBuckets = filteredBuckets.length;
  const totalPages = Math.ceil(totalBuckets / bucketsPerPage);
  const paginatedBuckets = useMemo(() => {
    const startIndex = (currentPage - 1) * bucketsPerPage;
    return filteredBuckets.slice(startIndex, startIndex + bucketsPerPage);
  }, [filteredBuckets, currentPage]);

  const handleClearFilters = () => {
    setSearchTerm("");
    setZoneFilter("All");
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
            onClick={() => void loadBuckets(true)}
            disabled={loading || wsLoading}
            variant="outline"
            size="sm"
            className="font-bold cursor-pointer transition-colors"
          >
            <RefreshCw className={loading ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} />
            <span>Sync</span>
          </Button>
        </div>
      </div>

      {/* 2. Filters & List Table */}
      <div className="flex flex-col gap-4">
        <BucketFilters
          searchTerm={searchTerm}
          setSearchTerm={setSearchTerm}
          zoneFilter={zoneFilter}
          setZoneFilter={setZoneFilter}
          statusFilter={statusFilter}
          setStatusFilter={setStatusFilter}
          uniqueZones={zones.map((z) => ({ id: z.id, name: z.name }))}
          handleClearFilters={handleClearFilters}
          setCurrentPage={setCurrentPage}
          onCreateClick={() => setIsCreateOpen(true)}
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
          zoneMap={zoneMap}
        />
      </div>

      {/* 3. Create Modal */}
      <CreateBucketModal
        isOpen={isCreateOpen}
        onClose={() => setIsCreateOpen(false)}
        onSuccess={() => void loadBuckets(true)}
        zones={zones}
      />
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
