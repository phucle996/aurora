import React, { useState, useMemo } from "react";
import {
  KeyRound,
  Plus,
  Trash2,
  Copy,
  Check,
  ShieldAlert,
  Loader2,
  CheckSquare,
  FileCode,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { toast } from "sonner";
import {
  listCredentials,
  createCredential,
  deleteCredential,
  type CredentialItem,
  type BucketItem,
} from "@/lib/api/storage";
import { cn } from "@/lib/utils";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { PolicyViewModal } from "./PolicyViewModal";
import { DeleteKeyModal } from "./DeleteKeyModal";
import { GenerateCredentialModal } from "./GenerateCredentialModal";

interface CredentialsTabProps {
  bucket: BucketItem;
}

const READ_WRITE_POLICY = `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:*"
      ],
      "Resource": [
        "arn:aws:s3:::*"
      ]
    }
  ]
}`;

const READ_ONLY_POLICY = `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:Get*",
        "s3:List*"
      ],
      "Resource": [
        "arn:aws:s3:::*"
      ]
    }
  ]
}`;

export function CredentialsTab({ bucket }: CredentialsTabProps) {
  const queryClient = useQueryClient();

  // Modal states for creating key
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [createdResult, setCreatedResult] = useState<CredentialItem | null>(null);

  // Copy states for active table rows
  const [copiedRowKey, setCopiedRowKey] = useState<string | null>(null);

  // Policy view state
  const [viewingPolicy, setViewingPolicy] = useState<string | null>(null);

  // Delete state
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [deletingCred, setDeletingCred] = useState<CredentialItem | null>(null);

  // [COMMENT]: Sử dụng useQuery từ TanStack Query để quản lý danh sách credentials.
  // Tự động gộp request, lưu cache và đồng bộ dữ liệu.
  const {
    data: credentials = [],
    isLoading: loading,
    refetch: fetchKeys,
  } = useQuery<CredentialItem[]>({
    // [COMMENT]: Đổi sang bucket.id theo snake_case của backend
    queryKey: ["credentials", bucket.id],
    queryFn: () => listCredentials(bucket.id),
    enabled: !!bucket.id,
  });

  const copyToClipboard = (text: string, rowId: string) => {
    navigator.clipboard.writeText(text);
    setCopiedRowKey(rowId);
    setTimeout(() => setCopiedRowKey(null), 1500);
    toast.success("Copied to clipboard");
  };

  // [COMMENT]: Mutation tạo access key mới, tự động invalidate query cache sau khi tạo thành công.
  const createCredentialMutation = useMutation<CredentialItem, Error, string>({
    // [COMMENT]: Đổi sang bucket.id theo snake_case của backend
    mutationFn: (policy) => createCredential(bucket.name, policy),
    onSuccess: (res) => {
      setCreatedResult(res);
      toast.success("Access credential generated successfully");
      // [COMMENT]: Đổi sang bucket.id theo snake_case của backend
      queryClient.invalidateQueries({ queryKey: ["credentials", bucket.id] });
    },
    onError: (err: any) => {
      toast.error(err.message || "Failed to generate credentials");
    },
  });

  const creatingKey = createCredentialMutation.isPending;

  const handleGenerateKey = (policy: string) => {
    createCredentialMutation.mutate(policy);
  };

  // [COMMENT]: Mutation xóa access key, tự động update local cache để nâng cao UX (Zero-Request UI update).
  const deleteCredentialMutation = useMutation<void, Error, string>({
    mutationFn: (id) => deleteCredential(bucket.id, id),
    onMutate: async (id) => {
      setDeletingId(id);
    },
    onSuccess: (_, id) => {
      toast.success("Access Key successfully deleted");
      // [COMMENT]: Đổi sang bucket.id theo snake_case của backend
      queryClient.setQueryData<CredentialItem[]>(["credentials", bucket.id], (prev) => {
        if (!prev) return [];
        return prev.filter((item) => item.id !== id);
      });
    },
    onError: (err: any) => {
      toast.error(err.message || "Failed to delete credential");
    },
    onSettled: () => {
      setDeletingId(null);
    },
  });

  // [COMMENT]: Xử lý sự kiện xác nhận xóa Access Key từ Modal
  const handleDelete = async (id: string) => {
    try {
      await deleteCredentialMutation.mutateAsync(id);
      setDeletingCred(null);
    } catch {
      // Bắt lỗi để tránh Unhandled Promise Rejection (đã có toast thông báo trong onError)
    }
  };

  const handleCloseModal = () => {
    setIsModalOpen(false);
    setCreatedResult(null);
    // [COMMENT]: Đổi sang bucket.id theo snake_case của backend
    queryClient.invalidateQueries({ queryKey: ["credentials", bucket.id] });
  };

  const getPolicyName = (policyJSON: string) => {
    if (policyJSON === READ_WRITE_POLICY) return "ReadWrite";
    if (policyJSON === READ_ONLY_POLICY) return "ReadOnly";
    return "Custom";
  };

  return (
    <div className="space-y-6 text-xs py-4 select-none">

      {/* Tab Header Toolbar */}
      <div className="flex items-center justify-between border-b border-border pb-3.5">
        <div className="flex items-center gap-2">
          <KeyRound className="h-4.5 w-4.5 text-muted-foreground" />
          <div className="flex flex-col">
            <span className="font-bold text-foreground">Access Key Credentials</span>
            <span className="text-[10px] text-muted-foreground mt-0.5">
              Secure keys for programmatically accessing objects in this bucket.
            </span>
          </div>
        </div>
        <Button
          onClick={() => setIsModalOpen(true)}
          size="sm"
          className="font-bold flex items-center gap-1.5 cursor-pointer bg-blue-600 hover:bg-blue-700 text-white rounded-md h-8"
        >
          <Plus className="h-4 w-4" />
          <span>Generate Key</span>
        </Button>
      </div>

      {/* Main Table */}
      {loading ? (
        <div className="flex flex-col items-center justify-center py-20 text-muted-foreground">
          <Loader2 className="h-7 w-7 animate-spin text-blue-500 mb-2.5" />
          <span className="text-[11px] font-semibold tracking-wider">Syncing Credentials...</span>
        </div>
      ) : credentials.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-16 text-center text-muted-foreground bg-muted/5 border border-border border-dashed rounded-xl">
          <KeyRound className="h-10 w-10 text-muted-foreground/50 mb-2.5" />
          <p className="font-bold text-sm">No access keys generated</p>
          <p className="text-[11px] mt-1 max-w-xs text-muted-foreground">
            Generate an access key credential pair to connect third party clients or applications.
          </p>
        </div>
      ) : (
        <div className="overflow-x-auto rounded-xl border border-border">
          <table className="w-full text-left border-collapse table-auto">
            <thead>
              <tr className="border-b border-border bg-muted/20 text-[10px] font-extrabold uppercase tracking-wider text-muted-foreground select-none">
                <th className="px-6 py-3.5">Access Key ID</th>
                <th className="px-6 py-3.5">Access Policy</th>
                <th className="px-6 py-3.5">Generated At</th>
                <th className="px-6 py-3.5 text-right pr-6">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border text-[13px]">
              {credentials.map((cred) => {
                const isRW = cred.policy === READ_WRITE_POLICY;
                const isRO = cred.policy === READ_ONLY_POLICY;
                return (
                  <tr key={cred.id} className="hover:bg-muted/40 transition-colors">

                    {/* Access Key */}
                    <td className="px-6 py-3.5">
                      <div className="flex items-center gap-2">
                        <span className="font-mono font-bold text-foreground text-xs select-all">
                          {cred.access_key}
                        </span>
                        <button
                          onClick={() => copyToClipboard(cred.access_key, cred.id)}
                          className="text-muted-foreground/60 hover:text-foreground cursor-pointer outline-none shrink-0"
                        >
                          {copiedRowKey === cred.id ? (
                            <Check className="h-3 w-3 text-emerald-500" />
                          ) : (
                            <Copy className="h-3 w-3" />
                          )}
                        </button>
                      </div>
                    </td>

                    {/* Policy Badge */}
                    <td className="px-6 py-3.5">
                      <div className="flex items-center gap-2">
                        <Badge
                          variant="outline"
                          className={cn(
                            "text-[9px] font-extrabold px-1.5 py-0.2 border capitalize",
                            isRW
                              ? "bg-blue-500/10 text-blue-600 border-blue-500/20"
                              : isRO
                                ? "bg-emerald-500/10 text-emerald-600 border-emerald-500/20"
                                : "bg-amber-500/10 text-amber-600 border-amber-500/20"
                          )}
                        >
                          {getPolicyName(cred.policy)}
                        </Badge>
                        <button
                          onClick={() => setViewingPolicy(cred.policy)}
                          className="text-blue-500 hover:text-blue-600 dark:text-blue-400 dark:hover:text-blue-300 font-semibold text-[11px] flex items-center gap-0.5 cursor-pointer outline-none"
                        >
                          <FileCode className="h-3 w-3" />
                          <span>View Policy</span>
                        </button>
                      </div>
                    </td>

                    {/* Created Date */}
                    <td className="px-6 py-3.5 text-slate-400 dark:text-slate-500">
                      {new Date(cred.created_at).toLocaleString()}
                    </td>

                    {/* Actions */}
                    <td className="px-6 py-3.5 text-right pr-6">
                      <Button
                        variant="ghost"
                        onClick={() => setDeletingCred(cred)}
                        disabled={deletingId === cred.id}
                        className="flex items-center gap-1 hover:text-red-500 transition-colors disabled:opacity-50"
                      >
                        <X className="h-3.5 w-3.5" />
                        <span>Delete</span>
                      </Button>
                    </td>

                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* MODAL 1: Generate Access Key */}
      <GenerateCredentialModal
        isOpen={isModalOpen}
        onClose={handleCloseModal}
        onSubmit={handleGenerateKey}
        isPending={creatingKey}
        createdResult={createdResult}
        bucketName={bucket.name}
      />

      {/* POLICY VIEW PANEL (EXTRACTED COMPONENT) */}
      <PolicyViewModal
        policy={viewingPolicy}
        onClose={() => setViewingPolicy(null)}
      />

      {/* DELETE KEY CONFIRMATION MODAL */}
      <DeleteKeyModal
        accessKey={deletingCred ? deletingCred.access_key : null}
        isDeleting={!!deletingId}
        onConfirm={() => deletingCred && handleDelete(deletingCred.id)}
        onClose={() => setDeletingCred(null)}
      />

    </div>
  );
}
