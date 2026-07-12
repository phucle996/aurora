"use client";

import React, { useState, useMemo } from "react";
import { useRouter } from "next/navigation";
import {
  HardDrive,
  ArrowLeft,
  X,
  Copy,
  Check,
  ShieldAlert,
  KeyRound,
  CheckSquare,
  Loader2,
  DollarSign,
  Info,
  ExternalLink,
  Download
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";
import { createBucket, type CreatedBucketResult, listBucketNames } from "@/lib/api/storage";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import RouteGuard from "@/components/route-guard";
import { useWorkspace } from "@/context/WorkspaceContext";
import { cn } from "@/lib/utils";

function CreateBucketContent() {
  const router = useRouter();
  const { activeWorkspaceID } = useWorkspace();
  const queryClient = useQueryClient();

  const [step, setStep] = useState<"form" | "result">("form");

  // Form states
  const [name, setName] = useState("");
  const [quotaGB, setQuotaGB] = useState<number>(50);

  // Result state
  const [result, setResult] = useState<CreatedBucketResult | null>(null);
  const [copiedAccess, setCopiedAccess] = useState(false);
  const [copiedSecret, setCopiedSecret] = useState(false);
  const [confirmedSave, setConfirmedSave] = useState(false);

  // [COMMENT]: Query lấy danh sách tên bucket cá nhân (lightweight API).
  // Chỉ kích hoạt tự động nếu trong cache của React Query chưa có sẵn danh sách đầy đủ.
  // Nhờ đó, nếu đi từ trang List sang, cache đã có sẵn và hoàn toàn 0 tốn thêm request nào.
  const hasFullCache = !!queryClient.getQueryData(["buckets", activeWorkspaceID]);

  const { data: bucketNames, isLoading: isNamesLoading } = useQuery<string[]>({
    queryKey: ["bucket-names", activeWorkspaceID],
    queryFn: () => listBucketNames(),
    enabled: !hasFullCache && !!activeWorkspaceID,
    // [COMMENT]: Chỉ cache nhẹ trong 1 phút, phục vụ check realtime trên page này
    staleTime: 60000,
  });

  // [COMMENT]: Lấy danh sách kiểm tra trùng lặp từ nguồn tối ưu nhất (Cache đầy đủ hoặc rút gọn)
  const existingBucketsList = useMemo(() => {
    if (hasFullCache) {
      const fullBuckets = queryClient.getQueryData<any[]>(["buckets", activeWorkspaceID]);
      return fullBuckets?.map((b) => b.Name) || [];
    }
    return bucketNames || [];
  }, [hasFullCache, bucketNames, activeWorkspaceID, queryClient]);

  // [COMMENT]: Kiểm tra sự trùng lặp thời gian thực dựa theo tên vật lý (gồm prefix của workspace)
  const isDuplicateName = useMemo(() => {
    if (!name || !activeWorkspaceID) return false;
    const physicalPrefix = `ws-${activeWorkspaceID.slice(0, 8)}-`;
    const targetPhysicalName = `${physicalPrefix}${name}`;
    return existingBucketsList.includes(targetPhysicalName);
  }, [name, activeWorkspaceID, existingBucketsList]);

  // [COMMENT]: Mutation sử dụng TanStack Query gọi API tạo bucket
  const createBucketMutation = useMutation<CreatedBucketResult, Error, { name: string; quotaBytes: number }>({
    mutationFn: ({ name, quotaBytes }) => createBucket(name, quotaBytes),
    onSuccess: (res) => {
      setResult(res);
      setStep("result");
      toast.success("Storage bucket created successfully!");
    },
    onError: (err: any) => {
      toast.error(err.message || "Failed to create storage bucket");
    },
  });

  const loading = createBucketMutation.isPending;

  const handleNameChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    // Chỉ chấp nhận chữ thường, số, dấu gạch ngang và dấu chấm
    const value = e.target.value.toLowerCase().replace(/[^a-z0-9.-]/g, "");
    setName(value);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      toast.error("Bucket name is required");
      return;
    }
    if (isDuplicateName) {
      toast.error("Bucket name already exists in this workspace");
      return;
    }
    if (quotaGB <= 0) {
      toast.error("Capacity quota must be greater than 0 GB");
      return;
    }

    const quotaBytes = quotaGB * 1024 * 1024 * 1024;
    createBucketMutation.mutate({ name, quotaBytes });
  };

  const copyToClipboard = (text: string, type: "access" | "secret") => {
    navigator.clipboard.writeText(text);
    if (type === "access") {
      setCopiedAccess(true);
      setTimeout(() => setCopiedAccess(false), 1500);
    } else {
      setCopiedSecret(true);
      setTimeout(() => setCopiedSecret(false), 1500);
    }
    toast.success("Copied to clipboard");
  };

  const handleFinalize = () => {
    // Reset states và quay về trang tổng quan storage
    router.push("/storage");
  };

  // [COMMENT]: Hàm hỗ trợ tải file JSON chứa toàn bộ thông tin credential vừa tạo để lưu trữ an toàn
  const downloadJSON = () => {
    if (!result) return;
    
    let parsedPolicy = {};
    try {
      parsedPolicy = JSON.parse(result.policy || "{}");
    } catch {
      parsedPolicy = result.policy;
    }

    const dataStr = JSON.stringify({
      bucket_id: result.bucket_id,
      bucket_name: result.bucket_name,
      access_key: result.access_key,
      secret_key: result.secret_key,
      policy: parsedPolicy,
    }, null, 2);

    const blob = new Blob([dataStr], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `aurora-${result.bucket_name}-credentials.json`;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    URL.revokeObjectURL(url);
    toast.success("Downloaded credentials JSON file");
  };

  // [COMMENT]: Tính toán chi phí động dựa trên GB nhập vào ($0.015 / GB / tháng)
  const estimatedCost = (quotaGB * 0.015).toFixed(2);

  return (
    <div className="space-y-6 pb-10 w-full text-foreground">

      {/* 1. Header Area */}
      <div className="flex items-center gap-3 border-b border-border pb-5 select-none">
        <button
          onClick={() => router.push("/storage")}
          className="flex items-center justify-center h-8 w-8 rounded-lg border border-border hover:bg-muted text-muted-foreground hover:text-foreground transition-colors cursor-pointer"
        >
          <ArrowLeft className="h-4 w-4" />
        </button>
        <div className="flex items-start gap-2.5">
          <div className="h-9 w-9 flex items-center justify-center rounded-xl bg-blue-600/10 border border-blue-500/20 text-blue-500">
            <HardDrive className="h-5 w-5" />
          </div>
          <div>
            <h1 className="text-xl font-bold tracking-tight text-foreground">
              {step === "form" ? "Create Storage Bucket" : "Bucket Credentials"}
            </h1>
            <p className="mt-0.5 text-xs text-muted-foreground font-semibold">
              {step === "form" ? "Configure your object storage bucket, scale capacity, and inspect pricing structure." : "Safeguard your cryptographic keys. You cannot view the secret key again."}
            </p>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">

        {/* ======================================================== */}
        {/* CỘT TRÁI (Form cấu hình / Hoặc Kết quả credentials) */}
        {/* ======================================================== */}
        <div className="lg:col-span-8 space-y-6 self-start">
          {step === "form" ? (
            <form onSubmit={handleSubmit} className="bg-card text-card-foreground border border-border rounded-lg shadow-xs overflow-hidden self-start">
              <div className="p-6 space-y-6 text-xs">

                {/* SECTION 1: Configuration */}
                <div className="border-b border-border/60 pb-5">
                  <span className="text-[11px] font-bold text-foreground uppercase tracking-wider block mb-4">
                    Configuration
                  </span>

                  <div className="space-y-4">
                    {/* Name */}
                    <div className="flex flex-col gap-1.5">
                      <label className="font-bold text-foreground select-none">
                        Bucket Name *
                      </label>
                      <div className="relative">
                        <input
                          type="text"
                          placeholder="e.g. static-assets-bucket"
                          value={name}
                          onChange={handleNameChange}
                          required
                          maxLength={63}
                          disabled={loading}
                          className={cn(
                            "w-full h-9 pl-3 pr-9 bg-background border rounded-md focus:outline-none focus:border-blue-500 text-foreground placeholder:text-muted-foreground/30 transition-colors",
                            isDuplicateName ? "border-red-500/80 focus:border-red-500" : "border-border"
                          )}
                        />
                        {/* Biểu tượng chỉ báo trạng thái kiểm tra trùng lặp thời gian thực */}
                        {name && (
                          <div className="absolute right-3 top-1/2 -translate-y-1/2 flex items-center justify-center select-none">
                            {isNamesLoading ? (
                              <Loader2 className="h-3.5 w-3.5 animate-spin text-muted-foreground/50" />
                            ) : isDuplicateName ? (
                              <X className="h-4 w-4 text-red-500" />
                            ) : name.length >= 3 ? (
                              <Check className="h-4 w-4 text-emerald-500" />
                            ) : null}
                          </div>
                        )}
                      </div>
                      <span className="text-[10px] text-muted-foreground leading-normal mt-0.5 select-none">
                        Must be unique workspace-wide. Lowercase letters, numbers, hyphens (-), and dots (.) only. Length 3-63.
                      </span>
                      {isDuplicateName && (
                        <span className="text-[10px] text-red-500 font-bold leading-normal mt-0.5 flex items-center gap-1 select-none animate-pulse">
                          <ShieldAlert className="h-3.5 w-3.5 text-red-500" />
                          <span>Bucket name already exists in this workspace.</span>
                        </span>
                      )}
                    </div>

                    {/* Quota limit */}
                    <div className="flex flex-col gap-1.5">
                      <label className="font-bold text-foreground select-none">
                        Capacity Limit (GB) *
                      </label>
                      <div className="flex items-center gap-3">
                        <input
                          type="number"
                          min={1}
                          max={100000}
                          value={quotaGB}
                          onChange={(e) => setQuotaGB(parseInt(e.target.value) || 0)}
                          required
                          disabled={loading}
                          className="w-40 h-9 px-3 bg-background border border-border rounded-md focus:outline-none focus:border-blue-500 text-foreground transition-colors"
                        />
                        <span className="text-[11px] font-bold text-muted-foreground">GB</span>
                      </div>
                      <span className="text-[10px] text-muted-foreground leading-normal mt-0.5">
                        Maximum storage space allocated for this bucket. You can expand this capacity at any time.
                      </span>
                    </div>
                  </div>
                </div>

                {/* SECTION 2: Advanced */}
                <div>
                  <span className="text-[11px] font-bold text-foreground uppercase tracking-wider block mb-2">
                    Advanced
                  </span>
                  <p className="text-[11px] text-muted-foreground font-medium italic">
                    No settings available yet.
                  </p>
                </div>

              </div>

              {/* Submit & Cancel Actions */}
              <div className="flex items-center justify-end gap-2 px-6 py-4 bg-muted/10 border-t border-border">
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => router.push("/storage")}
                  disabled={loading}
                  className="h-8.5 text-xs font-bold transition-colors cursor-pointer border-border text-foreground hover:bg-muted"
                >
                  Cancel
                </Button>
                <Button
                  type="submit"
                  disabled={loading || !name.trim() || isDuplicateName}
                  className="h-8.5 text-xs font-bold bg-blue-600 hover:bg-blue-700 text-white rounded-md flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
                >
                  {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                  <span>Create Bucket</span>
                </Button>
              </div>
            </form>
          ) : (
            <div className="bg-card text-card-foreground border border-border rounded-lg shadow-xs p-6 space-y-5 text-xs select-none self-start">

              {/* Warning Alert banner */}
              <div className="rounded-lg border border-amber-500/25 bg-amber-505/5 p-4 text-amber-800 dark:text-amber-300 leading-relaxed flex gap-3">
                <ShieldAlert className="h-5 w-5 shrink-0 text-amber-500" />
                <div>
                  <p className="font-bold">Store these credentials safely!</p>
                  <p className="mt-1 text-[11px] font-medium opacity-90 leading-normal">
                    The **Secret Key** will only be shown this **one time**. If you close this page without saving it, you will have to generate a new key pair or delete the bucket.
                  </p>
                </div>
              </div>

              {/* Credentials Fields */}
              <div className="space-y-4">
                <div className="flex justify-between items-center select-none">
                  <span className="text-[11px] font-bold text-foreground uppercase tracking-wider">
                    Credentials Info
                  </span>
                  <Button
                    type="button"
                    onClick={downloadJSON}
                    className="h-7.5 px-3 text-[10px] font-bold bg-blue-600 hover:bg-blue-700 text-white rounded-md flex items-center gap-1.5 cursor-pointer shadow-sm hover:shadow transition-all"
                  >
                    <Download className="h-3.5 w-3.5" />
                    <span>Download JSON</span>
                  </Button>
                </div>

                {/* Bucket ID */}
                <div className="flex flex-col gap-1.5">
                  <span className="font-bold text-foreground">Bucket ID</span>
                  <div className="flex h-9 items-center justify-between border border-border px-3 bg-muted/40 rounded-md font-mono text-[11px] text-slate-655 dark:text-slate-400">
                    <span className="truncate">{result?.bucket_id}</span>
                  </div>
                </div>

                {/* Access Key */}
                <div className="flex flex-col gap-1.5">
                  <span className="font-bold text-foreground">Access Key</span>
                  <div className="flex h-9 items-center justify-between border border-border pl-3 pr-1 bg-muted/10 rounded-md font-mono text-[11px] text-foreground">
                    <span className="truncate font-semibold select-all">{result?.access_key}</span>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      onClick={() => result && copyToClipboard(result.access_key, "access")}
                      className="h-7 w-7 text-muted-foreground hover:text-foreground cursor-pointer transition-colors"
                    >
                      {copiedAccess ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                    </Button>
                  </div>
                </div>

                {/* Secret Key */}
                <div className="flex flex-col gap-1.5">
                  <span className="font-bold text-foreground">Secret Key</span>
                  <div className="flex h-9 items-center justify-between border border-border pl-3 pr-1 bg-muted/10 rounded-md font-mono text-[11px] text-foreground">
                    <span className="truncate font-bold text-blue-600 dark:text-blue-400 select-all">{result?.secret_key}</span>
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      onClick={() => result && copyToClipboard(result.secret_key, "secret")}
                      className="h-7 w-7 text-muted-foreground hover:text-foreground cursor-pointer transition-colors"
                    >
                      {copiedSecret ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                    </Button>
                  </div>
                </div>

                {/* JSON Policy */}
                <div className="flex flex-col gap-1.5">
                  <span className="font-bold text-foreground">Default Access Policy</span>
                  {/* [COMMENT]: Sử dụng màu nền tối rõ ràng, màu chữ slate sáng tương phản cao và loại bỏ max-height để hiển thị trọn vẹn policy không bị scroll */}
                  <pre className="p-3.5 bg-slate-900 dark:bg-slate-950 text-slate-100 dark:text-slate-200 rounded-md font-mono text-[10px] overflow-x-auto border border-slate-800 leading-normal select-text">
                    {result?.policy}
                  </pre>
                </div>

              </div>

              {/* Checkbox confirmation */}
              <div className="pt-4 border-t border-border">
                <label className="flex items-start gap-2.5 cursor-pointer text-[11px] font-semibold text-slate-700 dark:text-slate-300">
                  <input
                    type="checkbox"
                    checked={confirmedSave}
                    onChange={(e) => setConfirmedSave(e.target.checked)}
                    className="mt-0.5 h-3.5 w-3.5 rounded border-border text-blue-600 focus:ring-blue-500 cursor-pointer"
                  />
                  <span className="leading-snug">
                    I have copied and stored the Access Key and Secret Key in a secure place.
                  </span>
                </label>
              </div>

              {/* Close action */}
              <div className="flex items-center justify-end pt-2 border-t border-border">
                <Button
                  type="button"
                  disabled={!confirmedSave}
                  onClick={handleFinalize}
                  className="h-8.5 px-6 text-xs font-bold bg-emerald-600 hover:bg-emerald-700 text-white rounded-md flex items-center gap-1.5 cursor-pointer disabled:opacity-40"
                >
                  <CheckSquare className="h-4 w-4" />
                  <span>Save & Close</span>
                </Button>
              </div>
            </div>
          )}
        </div>

        {/* ======================================================== */}
        {/* CỘT PHẢI (Thông tin giá cả & Tính toán chi phí) */}
        {/* ======================================================== */}
        <div className="lg:col-span-4 space-y-6 select-none self-start">

          {/* Box tính tiền động (Dynamic Billing Calculator Card) */}
          <div className="bg-card text-card-foreground border border-border rounded-lg shadow-xs p-6 space-y-4">
            <h3 className="text-xs font-bold uppercase tracking-wider text-foreground border-b border-border pb-2 flex items-center gap-1.5">
              <DollarSign className="h-4 w-4 text-emerald-500" />
              <span>Billing Calculator</span>
            </h3>

            <div className="flex flex-col items-center justify-center py-4 bg-emerald-500/5 dark:bg-emerald-500/2 rounded-lg border border-emerald-500/10">
              <span className="text-[10px] font-bold text-emerald-600 dark:text-emerald-400 uppercase tracking-widest">
                Estimated Cost
              </span>
              <div className="flex items-baseline mt-1.5 text-foreground">
                <span className="text-3xl font-extrabold font-mono">{estimatedCost}</span>
                <span className="text-xs font-bold text-muted-foreground ml-1">/ month</span>
              </div>
              <span className="text-[9px] text-muted-foreground/80 mt-1 font-semibold">
                Based on {quotaGB} GB allocated storage
              </span>
            </div>

            {/* Chi tiết bảng giá */}
            <div className="space-y-3 pt-2 text-xs">
              <span className="font-bold text-foreground text-[10px] uppercase tracking-wider text-muted-foreground block">
                Object Storage Rates
              </span>

              <div className="flex flex-col gap-2">
                <div className="flex justify-between items-center border-b border-border/30 pb-2">
                  <span className="text-[11px] font-semibold text-muted-foreground">Standard Storage</span>
                  <span className="font-semibold text-foreground font-mono">$0.015 / GB / month</span>
                </div>
                <div className="flex justify-between items-center border-b border-border/30 pb-2">
                  <span className="text-[11px] font-semibold text-muted-foreground">Inbound Data (Ingress)</span>
                  <span className="font-bold text-emerald-500 font-mono">Free</span>
                </div>
                <div className="flex justify-between items-center border-b border-border/30 pb-2">
                  <span className="text-[11px] font-semibold text-muted-foreground">Outbound Data (Egress)</span>
                  <span className="font-semibold text-foreground font-mono">$0.05 / GB</span>
                </div>
                <div className="flex justify-between items-center border-b border-border/30 pb-2">
                  <span className="text-[11px] font-semibold text-muted-foreground">Write Requests (PUT/COPY)</span>
                  <span className="font-semibold text-foreground font-mono">$0.005 / 10k reqs</span>
                </div>
                <div className="flex justify-between items-center pb-1">
                  <span className="text-[11px] font-semibold text-muted-foreground">Read Requests (GET/SELECT)</span>
                  <span className="font-semibold text-foreground font-mono">$0.004 / 10k reqs</span>
                </div>
              </div>
            </div>
          </div>

          {/* Card So sánh chi phí / Lợi ích */}
          <div className="bg-card text-card-foreground border border-border rounded-lg shadow-xs p-6 space-y-3.5">
            <h4 className="text-[10px] font-bold uppercase tracking-wider text-foreground flex items-center gap-1.5">
              <Info className="h-4 w-4 text-blue-500" />
              <span>Why Aurora Storage?</span>
            </h4>
            <p className="text-[11px] text-muted-foreground leading-relaxed font-semibold">
              Our storage charges are calculated pro-rata hourly based on storage volume limit. We do not charge for local internal transfers.
            </p>
            <div className="p-3 bg-muted/40 rounded-lg border border-border/60 text-[10px] leading-relaxed text-muted-foreground font-semibold">
              Compared to standard cloud storage providers ($0.023/GB), Aurora Object Storage saves you up to **35%** on your cloud storage bill.
            </div>
          </div>

        </div>

      </div>

    </div>
  );
}

export default function CreateBucketPage() {
  return (
    <RouteGuard requiredKey="storage:bucket" requiredAction="write">
      <CreateBucketContent />
    </RouteGuard>
  );
}
