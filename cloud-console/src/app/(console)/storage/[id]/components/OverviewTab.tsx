import React, { useState } from "react";
import { useRouter } from "next/navigation";
import { HardDrive, RefreshCw, Power, Trash2, Edit2, Check, Copy, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";
import { updateBucketQuota, suspendBucket, resumeBucket, deleteBucket, type BucketItem } from "@/lib/api/storage";
import { cn } from "@/lib/utils";

interface OverviewTabProps {
  bucket: BucketItem;
  onRefresh: () => void;
}

// [COMMENT]: Chuyển đổi dung lượng bytes sang GB
function bytesToGB(bytes: number): number {
  return Math.round(bytes / (1024 * 1024 * 1024));
}

export function OverviewTab({ bucket, onRefresh }: OverviewTabProps) {
  const router = useRouter();
  const [updatingQuota, setUpdatingQuota] = useState(false);
  const [newQuotaGB, setNewQuotaGB] = useState<number>(() => bytesToGB(bucket.CapacityQuotaBytes));
  const [showEditQuota, setShowEditQuota] = useState(false);

  const [togglingStatus, setTogglingStatus] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
  const [deleteConfirmName, setDeleteConfirmName] = useState("");
  const [copiedId, setCopiedId] = useState(false);

  // Mock static usage for dynamic visualization (state-of-the-art UI requirement)
  const mockUsedBytes = bucket.CapacityQuotaBytes * 0.248; // 24.8% mock usage
  const mockUsedGB = (mockUsedBytes / (1024 * 1024 * 1024)).toFixed(2);
  const totalGB = bytesToGB(bucket.CapacityQuotaBytes);
  const usagePercentage = Math.min((parseFloat(mockUsedGB) / totalGB) * 100, 100);

  const copyId = () => {
    navigator.clipboard.writeText(bucket.ID);
    setCopiedId(true);
    setTimeout(() => setCopiedId(false), 1500);
    toast.success("Bucket ID copied to clipboard");
  };

  const handleUpdateQuota = async (e: React.FormEvent) => {
    e.preventDefault();
    if (newQuotaGB <= 0) {
      toast.error("Quota must be greater than 0 GB");
      return;
    }
    setUpdatingQuota(true);
    try {
      const quotaBytes = newQuotaGB * 1024 * 1024 * 1024;
      await updateBucketQuota(bucket.ID, quotaBytes);
      toast.success("Storage quota limit updated successfully");
      setShowEditQuota(false);
      onRefresh();
    } catch (err: any) {
      toast.error(err.message || "Failed to update quota limit");
    } finally {
      setUpdatingQuota(false);
    }
  };

  const handleToggleStatus = async () => {
    setTogglingStatus(true);
    try {
      if (bucket.Status === "suspended") {
        await resumeBucket(bucket.ID);
        toast.success("Bucket resumed and is now active");
      } else {
        await suspendBucket(bucket.ID);
        toast.warning("Bucket suspended and is read-only/paused");
      }
      onRefresh();
    } catch (err: any) {
      toast.error(err.message || "Failed to toggle status");
    } finally {
      setTogglingStatus(false);
    }
  };

  const handleDelete = async (e: React.FormEvent) => {
    e.preventDefault();
    if (deleteConfirmName !== bucket.Name) {
      toast.error("Bucket name does not match confirmation value");
      return;
    }
    setDeleting(true);
    try {
      await deleteBucket(bucket.ID);
      toast.success("Bucket deletion sequence initiated");
      router.push("/storage");
    } catch (err: any) {
      toast.error(err.message || "Failed to delete bucket");
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="space-y-6 text-xs max-w-3xl py-4 select-none">

      {/* SECTION 1: Overview Summary Card */}
      <div className="border-b border-border/60 pb-5">
        <span className="text-[11px] font-bold text-foreground uppercase tracking-wider block mb-3">
          Storage Allocation
        </span>
        <div className="p-4 bg-muted/20 border border-border rounded-xl space-y-4">
          <div className="flex items-center justify-between">
            <span className="font-semibold text-slate-500">Allocated Quota Limit</span>
            <span className="font-mono font-bold text-slate-800 dark:text-slate-100 text-sm">
              {mockUsedGB} GB of {totalGB} GB used
            </span>
          </div>
          {/* Progress bar */}
          <div className="w-full bg-slate-200 dark:bg-slate-800 h-2.5 rounded-full overflow-hidden">
            <div
              style={{ width: `${usagePercentage}%` }}
              className="bg-blue-600 h-full rounded-full transition-all duration-500 ease-out"
            />
          </div>
          <div className="flex items-center justify-between text-[10px] text-muted-foreground font-semibold">
            <span>{usagePercentage.toFixed(1)}% Usage</span>
            <span>{(totalGB - parseFloat(mockUsedGB)).toFixed(2)} GB Available</span>
          </div>
        </div>
      </div>

      {/* SECTION 2: General Information */}
      <div className="border-b border-border/60 pb-5">
        <span className="text-[11px] font-bold text-foreground uppercase tracking-wider block mb-3">
          Metadata Details
        </span>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-y-4 gap-x-6 text-xs">

          <div className="flex flex-col gap-1">
            <span className="font-bold text-muted-foreground">Bucket ID</span>
            <div className="flex items-center gap-1.5 font-mono text-[11px]">
              <span className="text-foreground truncate max-w-[200px]">{bucket.ID}</span>
              <button
                onClick={copyId}
                className="text-muted-foreground/60 hover:text-foreground cursor-pointer outline-none"
              >
                {copiedId ? <Check className="h-3 w-3 text-emerald-500" /> : <Copy className="h-3 w-3" />}
              </button>
            </div>
          </div>



          <div className="flex flex-col gap-1">
            <span className="font-bold text-muted-foreground">Workspace ID</span>
            <span className="font-mono text-[11px] text-foreground">{bucket.WorkspaceID}</span>
          </div>

          <div className="flex flex-col gap-1">
            <span className="font-bold text-muted-foreground">Created At</span>
            <span className="font-semibold text-foreground">
              {new Date(bucket.CreatedAt).toLocaleString()}
            </span>
          </div>

        </div>
      </div>

      {/* SECTION 3: Resource Tuning Actions */}
      <div className="border-b border-border/60 pb-5 space-y-4">
        <span className="text-[11px] font-bold text-foreground uppercase tracking-wider block mb-1">
          Resource Actions
        </span>

        {/* Action A: Update Quota */}
        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 py-2">
          <div className="space-y-0.5 max-w-md">
            <h4 className="font-bold text-foreground text-sm">Resize Quota Limit</h4>
            <p className="text-muted-foreground text-[11px] leading-normal font-medium">
              Adjust the storage capacity boundary allocated for this bucket dynamically without interrupting active uploads.
            </p>
          </div>
          <div>
            {!showEditQuota ? (
              <Button
                variant="outline"
                onClick={() => setShowEditQuota(true)}
                className="h-8 text-xs font-bold border-border text-foreground hover:bg-muted cursor-pointer transition-colors"
              >
                <Edit2 className="h-3.5 w-3.5" />
                <span>Adjust Quota</span>
              </Button>
            ) : (
              <form onSubmit={handleUpdateQuota} className="flex items-center gap-2">
                <input
                  type="number"
                  min={1}
                  max={100000}
                  value={newQuotaGB}
                  onChange={(e) => setNewQuotaGB(parseInt(e.target.value) || 0)}
                  className="w-20 h-8 px-2 bg-background border border-border rounded-md text-xs focus:outline-none focus:border-blue-500 font-semibold"
                />
                <span className="font-bold text-muted-foreground mr-1">GB</span>
                <Button
                  type="submit"
                  disabled={updatingQuota || newQuotaGB === totalGB}
                  size="sm"
                  className="h-8 bg-blue-600 hover:bg-blue-700 text-white rounded-md font-bold cursor-pointer disabled:opacity-50"
                >
                  {updatingQuota ? "Saving..." : "Save"}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => {
                    setShowEditQuota(false);
                    setNewQuotaGB(totalGB);
                  }}
                  className="h-8 text-muted-foreground hover:text-foreground text-xs font-bold cursor-pointer"
                >
                  Cancel
                </Button>
              </form>
            )}
          </div>
        </div>

        {/* Action B: Suspend/Resume */}
        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 py-2 border-t border-border/40">
          <div className="space-y-0.5 max-w-md">
            <h4 className="font-bold text-foreground text-sm">
              {bucket.Status === "suspended" ? "Resume Storage Operations" : "Suspend Storage Operations"}
            </h4>
            <p className="text-muted-foreground text-[11px] leading-normal font-medium">
              {bucket.Status === "suspended"
                ? "Re-activate the bucket to allow clients to download and upload files normally."
                : "Temporarily freeze the bucket. Read and write actions will be disabled, but no files will be deleted."}
            </p>
          </div>
          <div>
            <Button
              variant="outline"
              onClick={handleToggleStatus}
              disabled={togglingStatus}
              className={cn(
                "h-8 text-xs font-bold transition-colors cursor-pointer flex items-center gap-1.5",
                bucket.Status === "suspended"
                  ? "border-emerald-200 dark:border-emerald-950 text-emerald-600 dark:text-emerald-450 hover:bg-emerald-50 dark:hover:bg-emerald-950/20"
                  : "border-amber-200 dark:border-amber-955 text-amber-600 dark:text-amber-450 hover:bg-amber-50 dark:hover:bg-amber-950/20"
              )}
            >
              <Power className="h-3.5 w-3.5" />
              <span>
                {togglingStatus ? "Toggling..." : bucket.Status === "suspended" ? "Resume Bucket" : "Suspend Bucket"}
              </span>
            </Button>
          </div>
        </div>

        {/* Action C: Delete Bucket */}
        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 py-2 border-t border-border/40">
          <div className="space-y-0.5 max-w-md">
            <h4 className="font-bold text-red-600 dark:text-red-400 text-sm">Danger Zone: Delete Bucket</h4>
            <p className="text-muted-foreground text-[11px] leading-normal font-medium">
              Permanently delete this bucket and all associated files. This action is irreversible. All access keys will be instantly revoked.
            </p>
          </div>
          <div>
            <Button
              variant="outline"
              onClick={() => setShowDeleteConfirm(true)}
              className="h-8 text-xs font-bold border-red-200 dark:border-red-950/50 text-red-655 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/20 transition-colors cursor-pointer flex items-center gap-1.5"
            >
              <Trash2 className="h-3.5 w-3.5" />
              <span>Delete Bucket</span>
            </Button>
          </div>
        </div>

        {/* Delete Confirmation Box (Inline according to design specs) */}
        {showDeleteConfirm && (
          <div className="rounded-lg p-4 bg-red-500/5 border border-red-500/20 text-red-800 dark:text-red-300 select-none animate-in fade-in slide-in-from-top-2 duration-200">
            <form onSubmit={handleDelete} className="space-y-3">
              <p className="font-bold text-foreground">Are you absolutely sure you want to delete this bucket?</p>
              <p className="text-[11px] leading-normal opacity-90">
                To confirm, type <span className="font-mono font-bold text-foreground select-all bg-muted/60 px-1 py-0.5 rounded">{bucket.Name}</span> below:
              </p>
              <div className="flex items-center gap-3">
                <input
                  type="text"
                  placeholder="Type bucket name here..."
                  value={deleteConfirmName}
                  onChange={(e) => setDeleteConfirmName(e.target.value)}
                  className="flex-1 max-w-sm h-8 px-3 bg-background border border-red-500/30 rounded-md text-xs focus:outline-none focus:border-red-500 text-foreground"
                  required
                />
                <Button
                  type="submit"
                  disabled={deleting || deleteConfirmName !== bucket.Name}
                  className="h-8 bg-red-600 hover:bg-red-750 text-white rounded-md font-bold cursor-pointer disabled:opacity-50 flex items-center gap-1"
                >
                  {deleting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                  <span>Confirm Delete</span>
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  onClick={() => {
                    setShowDeleteConfirm(false);
                    setDeleteConfirmName("");
                  }}
                  className="h-8 text-muted-foreground hover:text-foreground text-xs font-bold cursor-pointer"
                >
                  Cancel
                </Button>
              </div>
            </form>
          </div>
        )}

      </div>

    </div>
  );
}
