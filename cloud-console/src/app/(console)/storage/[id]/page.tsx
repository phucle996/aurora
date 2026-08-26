"use client";

import React, { useState, useMemo, useCallback } from "react";
import { useRouter, useParams } from "next/navigation";
import Link from "next/link";
import {
  HardDrive,
  ArrowLeft,
  Loader2,
  RefreshCw,
  Info,
  KeyRound,
  FolderOpen,
  Clock,
} from "lucide-react";
import { toast } from "sonner";
import { getBucketDetails, type BucketItem } from "@/features/storage/api";
import { useUserSession } from "@/session/use-session";
import RouteGuard from "@/components/route-guard";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useBucketSizesSync } from "@/features/storage/realtime";
import { useConsoleQueryScope } from "@/shared/query/scope";

import { OverviewTab } from "./components/OverviewTab";
import { CredentialsTab } from "./components/CredentialsTab";
import { ObjectsTab } from "./components/ObjectsTab";
import { LifecycleTab } from "./components/LifecycleTab";

function ViewBucketContent() {
  const router = useRouter();
  const { id } = useParams() as { id: string };
  const { checkPermission } = useUserSession();
  const scope = useConsoleQueryScope();

  const [activeTab, setActiveTab] = useState("Overview");

  const queryClient = useQueryClient();
  // [COMMENT]: Đăng ký lắng nghe sự kiện đồng bộ dung lượng từ Centrifugo WebSocket cho chi tiết bucket
  useBucketSizesSync(
    useCallback((updatedSizes: Record<string, string>) => {
      queryClient.setQueryData<BucketItem | null>(
        [...scope, "storage", "bucket", id],
        (prevBucket) => {
          if (!prevBucket) return null;
          if (updatedSizes[prevBucket.name] !== undefined) {
            return {
              ...prevBucket,
              used_mb: updatedSizes[prevBucket.name],
            };
          }
          return prevBucket;
        }
      );
    }, [queryClient, id, scope])
  );

  // [COMMENT]: Sử dụng useQuery từ TanStack Query để quản lý chi tiết bucket.
  // Tự động retry và cache dữ liệu, giảm thiểu gọi API dư thừa.
  const {
    data: bucket = null,
    isLoading: loading,
    isRefetching: refreshing,
    refetch: loadBucketDetails,
  } = useQuery<BucketItem | null>({
    queryKey: [...scope, "storage", "bucket", id],
    queryFn: async () => {
      if (!id) return null;
      try {
        return await getBucketDetails(id);
      } catch (err: unknown) {
        toast.error(err instanceof Error ? err.message : "Failed to load bucket details");
        router.push("/storage");
        return null;
      }
    },
    enabled: !!id,
  });

  const tabs = useMemo(() => {
    const list = ["Overview", "Objects", "Lifecycle"];
    if (checkPermission("storage:credential", "read")) {
      list.push("Credentials");
    }
    return list;
  }, [checkPermission]);

  const renderActiveTab = () => {
    if (!bucket) return null;
    switch (activeTab) {
      case "Overview":
        return (
          <OverviewTab
            bucket={bucket}
            onRefresh={() => void loadBucketDetails()}
          />
        );
      case "Objects":
        return <ObjectsTab bucket={bucket} />;
      case "Lifecycle":
        return (
          <LifecycleTab
            bucket={bucket}
            onRefresh={() => void loadBucketDetails()}
          />
        );
      case "Credentials":
        return <CredentialsTab bucket={bucket} />;
      default:
        return null;
    }
  };

  const getTabIcon = (tabName: string) => {
    switch (tabName) {
      case "Overview":
        return <Info className="h-3.5 w-3.5" />;
      case "Objects":
        return <FolderOpen className="h-3.5 w-3.5" />;
      case "Lifecycle":
        return <Clock className="h-3.5 w-3.5" />;
      case "Credentials":
        return <KeyRound className="h-3.5 w-3.5" />;
      default:
        return null;
    }
  };

  if (loading && !bucket) {
    return (
      <div className="flex flex-col items-center justify-center py-40 text-muted-foreground select-none">
        <Loader2 className="h-9 w-9 animate-spin text-blue-500 mb-3" />
        <span className="text-xs font-bold uppercase tracking-wider animate-pulse">
          Loading Bucket Profile...
        </span>
      </div>
    );
  }

  if (!bucket) return null;

  return (
    <div className="flex flex-col gap-6 w-full relative pb-10 text-foreground min-h-[calc(100vh-110px)] items-stretch px-6">

      {/* 1. Header Section with Back Arrow */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 border-b border-border pb-5">
        <div className="flex items-center gap-4">
          <Link
            href="/storage"
            className="h-9 w-9 flex items-center justify-center rounded-lg border border-border bg-background text-muted-foreground hover:text-foreground cursor-pointer transition-colors outline-none"
          >
            <ArrowLeft className="h-4 w-4" />
          </Link>
          <div className="flex flex-col">
            <div className="flex items-center gap-2.5">
              <div className="h-7 w-7 flex items-center justify-center rounded-lg bg-blue-600/10 text-blue-500 border border-blue-500/20">
                <HardDrive className="h-4 w-4" />
              </div>
              <h1 className="text-xl font-bold text-foreground tracking-tight select-all">
                {/* [COMMENT]: Đổi sang sử dụng thuộc tính lowercase 'name' theo backend */}
                {bucket.name}
              </h1>

            </div>
            <p className="text-[10px] text-muted-foreground mt-1 font-mono uppercase tracking-wider select-none">
              Physical Object Storage Bucket Detail Node
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button
            onClick={() => void loadBucketDetails()}
            disabled={loading || refreshing}
            variant="outline"
            size="sm"
            className="font-bold cursor-pointer transition-colors"
          >
            <RefreshCw className={loading || refreshing ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} />
            <span>Sync</span>
          </Button>
        </div>
      </div>

      {/* 2. Flat Tab Navigation Bar */}
      <div className="flex border-b border-border text-[13px] font-bold text-muted-foreground select-none gap-2">
        {tabs.map((tab) => (
          <button
            key={tab}
            onClick={() => setActiveTab(tab)}
            className={cn(
              "pb-2.5 px-3.5 border-b-2 -mb-[2px] transition-all cursor-pointer font-bold flex items-center gap-2 outline-none",
              activeTab === tab
                ? "border-blue-600 text-blue-600 dark:text-blue-450"
                : "border-transparent hover:text-foreground hover:border-slate-300 dark:hover:border-slate-800"
            )}
          >
            {getTabIcon(tab)}
            <span>{tab}</span>
          </button>
        ))}
      </div>

      {/* 3. Active Tab Render Container */}
      <div className="animate-in fade-in slide-in-from-bottom-2 duration-300">
        {renderActiveTab()}
      </div>

    </div>
  );
}

export default function ViewBucketPage() {
  return (
    <RouteGuard requiredKey="storage:bucket" requiredAction="read">
      <ViewBucketContent />
    </RouteGuard>
  );
}
