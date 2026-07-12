import React from "react";
import Link from "next/link";
import {
  Loader2,
  ShieldAlert,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  ArrowUpDown,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { type BucketItem } from "@/lib/api/storage";

interface BucketTableProps {
  loading: boolean;
  buckets: BucketItem[];
  filteredBuckets: BucketItem[];
  paginatedBuckets: BucketItem[];
  currentPage: number;
  setCurrentPage: React.Dispatch<React.SetStateAction<number>>;
  bucketsPerPage: number;
  totalBuckets: number;
  totalPages: number;
  sortKey: string;
  sortDir: "asc" | "desc";
  onSort: (key: string) => void;
}

// [COMMENT]: Chuyển đổi dung lượng bytes sang đơn vị GB dễ đọc
function formatQuota(bytes: number): string {
  if (bytes === 0) return "Unlimited";
  const gb = bytes / (1024 * 1024 * 1024);
  return `${gb.toFixed(0)} GB`;
}

// [COMMENT]: Định dạng thời gian tạo bucket
function formatDate(dateStr: string): string {
  try {
    const d = new Date(dateStr);
    if (isNaN(d.getTime())) return dateStr;
    const months = [
      "Jan", "Feb", "Mar", "Apr", "May", "Jun",
      "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"
    ];
    const now = new Date();
    if (d.toDateString() === now.toDateString()) {
      return "Today";
    }
    const yesterday = new Date();
    yesterday.setDate(now.getDate() - 1);
    if (d.toDateString() === yesterday.toDateString()) {
      return "Yesterday";
    }
    return `${months[d.getMonth()]} ${d.getDate().toString().padStart(2, "0")}`;
  } catch {
    return dateStr;
  }
}

export function BucketTable({
  loading,
  buckets,
  filteredBuckets,
  paginatedBuckets,
  currentPage,
  setCurrentPage,
  bucketsPerPage,
  totalBuckets,
  totalPages,
  sortKey,
  sortDir,
  onSort,
}: BucketTableProps) {
  const SortIcon = ({ col }: { col: string }) => (
    <ArrowUpDown
      className={cn(
        "h-3 w-3 ml-1 shrink-0 transition-colors",
        sortKey === col ? "text-foreground" : "text-muted-foreground/45"
      )}
    />
  );

  return (
    <>
      {loading && buckets.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-24 text-muted-foreground">
          <Loader2 className="h-8 w-8 animate-spin text-blue-500 mb-3" />
          <span className="text-xs font-semibold uppercase tracking-wider animate-pulse">
            Syncing Object Storage Buckets...
          </span>
        </div>
      ) : filteredBuckets.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-20 px-6 text-center text-muted-foreground select-none">
          <ShieldAlert className="h-12 w-12 text-muted-foreground/60 mb-3" />
          <p className="text-sm font-semibold">No storage buckets found</p>
          <p className="text-xs mt-1.5 max-w-xs text-muted-foreground leading-normal">
            Adjust your search filter or create a new bucket to start storing objects.
          </p>
        </div>
      ) : (
        <div className="overflow-x-auto select-none rounded-xl border border-border">
          <table className="w-full text-left border-collapse table-auto">
            <thead>
              <tr className="border-b border-border bg-muted/20 text-[10px] font-extrabold uppercase tracking-wider text-muted-foreground select-none font-sans">
                <th
                  className="px-6 py-3.5 cursor-pointer hover:text-foreground transition-colors"
                  onClick={() => onSort("name")}
                >
                  <span className="inline-flex items-center">
                    Bucket Name <SortIcon col="name" />
                  </span>
                </th>

                <th
                  className="px-6 py-3.5 cursor-pointer hover:text-foreground transition-colors"
                  onClick={() => onSort("status")}
                >
                  <span className="inline-flex items-center">
                    Status <SortIcon col="status" />
                  </span>
                </th>
                <th
                  className="px-6 py-3.5 cursor-pointer hover:text-foreground transition-colors"
                  onClick={() => onSort("quota")}
                >
                  <span className="inline-flex items-center">
                    Capacity Limit <SortIcon col="quota" />
                  </span>
                </th>
                <th
                  className="px-6 py-3.5 cursor-pointer hover:text-foreground transition-colors"
                  onClick={() => onSort("created_at")}
                >
                  <span className="inline-flex items-center">
                    Created At <SortIcon col="created_at" />
                  </span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border text-[13px]">
              {paginatedBuckets.map((b) => {
                return (
                  <tr
                    key={b.id}
                    className="hover:bg-muted/40 transition-colors select-none"
                  >
                    {/* Bucket Name */}
                    <td className="px-6 py-3.5">
                      <Link
                        href={`/storage/${b.id}`}
                        className="font-bold text-foreground text-blue-600 hover:text-blue-700 dark:text-blue-450 dark:hover:text-blue-300 hover:underline cursor-pointer outline-none"
                      >
                        {/* [COMMENT]: Đổi sang sử dụng thuộc tính lowercase 'name' theo backend */}
                        {b.name}
                      </Link>
                    </td>

                    {/* Status Dot */}
                    <td className="px-6 py-3.5">
                      <span className="inline-flex items-center gap-2 text-xs font-bold select-none">
                        <span
                          className={cn(
                            "h-1.5 w-1.5 rounded-full",
                            b.status === "active"
                              ? "bg-emerald-500 animate-pulse"
                              : b.status === "creating"
                              ? "bg-amber-500"
                              : b.status === "suspended"
                              ? "bg-red-500"
                              : "bg-slate-400"
                          )}
                        />
                        <span
                          className={cn(
                            "capitalize",
                            b.status === "active"
                              ? "text-emerald-600 dark:text-emerald-400"
                              : b.status === "creating"
                              ? "text-amber-600 dark:text-amber-400"
                              : b.status === "suspended"
                              ? "text-red-600 dark:text-red-400"
                              : "text-slate-500"
                          )}
                        >
                          {/* [COMMENT]: Đổi sang sử dụng thuộc tính lowercase 'status' theo backend */}
                          {b.status}
                        </span>
                      </span>
                    </td>

                    {/* Capacity Quota */}
                    <td className="px-6 py-3.5 font-semibold text-slate-750 dark:text-slate-350">
                      {/* [COMMENT]: Đổi sang sử dụng thuộc tính 'capacity_quota_bytes' */}
                      {formatQuota(b.capacity_quota_bytes)}
                    </td>

                    {/* Created At */}
                    <td className="px-6 py-3.5 text-slate-400 dark:text-slate-500">
                      {/* [COMMENT]: Đổi sang sử dụng thuộc tính 'created_at' */}
                      {formatDate(b.created_at)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Footer Pagination */}
      {filteredBuckets.length > 0 && (
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 border-t border-border p-4 bg-muted/20 text-xs font-semibold text-muted-foreground select-none">
          <div>
            Showing {Math.min((currentPage - 1) * bucketsPerPage + 1, totalBuckets)} to{" "}
            {Math.min(currentPage * bucketsPerPage, totalBuckets)} of {totalBuckets} buckets
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
