"use client";

import React, { useState, useMemo } from "react";
import { useRouter } from "next/navigation";
import { HardDrive, ArrowLeft } from "lucide-react";
import { toast } from "sonner";
import { createBucket, type CreatedBucketResult, listBucketNames } from "@/lib/api/storage";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import RouteGuard from "@/components/route-guard";
import { useWorkspace } from "@/context/WorkspaceContext";

// Import sub-components
import { BillingCalculator } from "./components/BillingCalculator";
import { CreatedBucketResultView } from "./components/CreatedBucketResultView";
import { CreateBucketForm } from "./components/CreateBucketForm";

const getReadWritePolicy = () => `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:*"
      ],
      "Resource": [
        "arn:aws:s3:::<BUCKET_NAME>",
        "arn:aws:s3:::<BUCKET_NAME>/*"
      ]
    }
  ]
}`;

const getReadOnlyPolicy = () => `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:Get*",
        "s3:List*"
      ],
      "Resource": [
        "arn:aws:s3:::<BUCKET_NAME>",
        "arn:aws:s3:::<BUCKET_NAME>/*"
      ]
    }
  ]
}`;

function CreateBucketContent() {
  const router = useRouter();
  const { activeWorkspaceID } = useWorkspace();
  const queryClient = useQueryClient();

  const [step, setStep] = useState<"form" | "result">("form");

  // Form states
  const [name, setName] = useState("");
  const [quotaGB, setQuotaGB] = useState<number>(50);
  const [selectedPolicyTemplate, setSelectedPolicyTemplate] = useState<"readwrite" | "readonly" | "custom">("readwrite");
  const [customPolicyText, setCustomPolicyText] = useState("");

  // Advanced configurations states
  const [encryptEnabled, setEncryptEnabled] = useState(false);
  const [versioningEnabled, setVersioningEnabled] = useState(false);
  const [objectLockingEnabled, setObjectLockingEnabled] = useState(false);
  const [replicationEnabled, setReplicationEnabled] = useState(false);
  const [retentionDays, setRetentionDays] = useState<number>(0);
  const [legalHoldEnabled, setLegalHoldEnabled] = useState(false);
  const [tags, setTags] = useState<Record<string, string>>({});

  // Initialize custom policy text template
  React.useEffect(() => {
    setCustomPolicyText(getReadWritePolicy());
  }, []);

  // Result state
  const [result, setResult] = useState<CreatedBucketResult | null>(null);

  const hasFullCache = !!queryClient.getQueryData(["buckets", activeWorkspaceID]);

  const { data: bucketNames, isLoading: isNamesLoading } = useQuery<string[]>({
    queryKey: ["bucket-names", activeWorkspaceID],
    queryFn: () => listBucketNames(),
    enabled: !hasFullCache && !!activeWorkspaceID,
    staleTime: 60000,
  });

  const existingBucketsList = useMemo(() => {
    if (hasFullCache) {
      const fullBuckets = queryClient.getQueryData<any[]>(["buckets", activeWorkspaceID]);
      return fullBuckets?.map((b) => b.name) || [];
    }
    return bucketNames || [];
  }, [hasFullCache, bucketNames, activeWorkspaceID, queryClient]);

  const isDuplicateName = useMemo(() => {
    if (!name || !activeWorkspaceID) return false;
    const physicalPrefix = `ws-${activeWorkspaceID.slice(0, 8)}-`;
    const targetPhysicalName = `${physicalPrefix}${name}`;
    return existingBucketsList.includes(targetPhysicalName);
  }, [name, activeWorkspaceID, existingBucketsList]);

  // [COMMENT]: Mutation sử dụng TanStack Query gọi API tạo bucket kèm tùy chọn nâng cao
  const createBucketMutation = useMutation<
    CreatedBucketResult,
    Error,
    {
      name: string;
      quotaBytes: number;
      policy: string;
      advancedOptions: {
        encrypt_enabled: boolean;
        versioning_enabled: boolean;
        object_locking_enabled: boolean;
        replication_enabled: boolean;
        retention_days: number;
        legal_hold_enabled: boolean;
        tags: Record<string, string>;
      };
    }
  >({
    mutationFn: ({ name, quotaBytes, policy, advancedOptions }) =>
      createBucket(name, quotaBytes, policy, advancedOptions),
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
    
    let policy = "";
    if (selectedPolicyTemplate === "readwrite") {
      policy = getReadWritePolicy();
    } else if (selectedPolicyTemplate === "readonly") {
      policy = getReadOnlyPolicy();
    } else {
      try {
        JSON.parse(customPolicyText);
        policy = customPolicyText;
      } catch (err) {
        toast.error("Custom JSON policy syntax is invalid");
        return;
      }
    }

    createBucketMutation.mutate({
      name,
      quotaBytes,
      policy,
      advancedOptions: {
        encrypt_enabled: encryptEnabled,
        versioning_enabled: versioningEnabled,
        object_locking_enabled: objectLockingEnabled,
        replication_enabled: replicationEnabled,
        retention_days: retentionDays,
        legal_hold_enabled: legalHoldEnabled,
        tags,
      },
    });
  };

  const handleFinalize = () => {
    router.push("/storage");
  };

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

        {/* CỘT TRÁI (Form cấu hình / Hoặc Kết quả credentials) */}
        <div className="lg:col-span-8 space-y-6 self-start animate-in fade-in duration-200">
          {step === "form" ? (
            <CreateBucketForm
              name={name}
              setName={setName}
              quotaGB={quotaGB}
              setQuotaGB={setQuotaGB}
              selectedPolicyTemplate={selectedPolicyTemplate}
              setSelectedPolicyTemplate={setSelectedPolicyTemplate}
              customPolicyText={customPolicyText}
              setCustomPolicyText={setCustomPolicyText}
              loading={loading}
              isNamesLoading={isNamesLoading}
              isDuplicateName={isDuplicateName}
              onSubmit={handleSubmit}
              onCancel={() => router.push("/storage")}
              getReadWritePolicy={getReadWritePolicy}
              getReadOnlyPolicy={getReadOnlyPolicy}
              
              encryptEnabled={encryptEnabled}
              setEncryptEnabled={setEncryptEnabled}
              versioningEnabled={versioningEnabled}
              setVersioningEnabled={setVersioningEnabled}
              objectLockingEnabled={objectLockingEnabled}
              setObjectLockingEnabled={setObjectLockingEnabled}
              replicationEnabled={replicationEnabled}
              setReplicationEnabled={setReplicationEnabled}
              retentionDays={retentionDays}
              setRetentionDays={setRetentionDays}
              legalHoldEnabled={legalHoldEnabled}
              setLegalHoldEnabled={setLegalHoldEnabled}
              tags={tags}
              setTags={setTags}
            />
          ) : (
            result && (
              <CreatedBucketResultView
                result={result}
                onFinalize={handleFinalize}
              />
            )
          )}
        </div>

        {/* CỘT PHẢI (Thông tin giá cả & Tính toán chi phí) */}
        <div className="lg:col-span-4 space-y-6 select-none self-start">
          <BillingCalculator quotaGB={quotaGB} />
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
