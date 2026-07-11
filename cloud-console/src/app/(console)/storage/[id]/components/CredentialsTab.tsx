import React, { useState, useEffect, useCallback } from "react";
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
  revokeCredential,
  type CredentialItem,
  type BucketItem,
} from "@/lib/api/storage";
import { cn } from "@/lib/utils";

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
  const [credentials, setCredentials] = useState<CredentialItem[]>([]);
  const [loading, setLoading] = useState(true);

  // Modal states for creating key
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [modalStep, setModalStep] = useState<"policy" | "result">("policy");
  const [selectedPolicyTemplate, setSelectedPolicyTemplate] = useState<"readwrite" | "readonly" | "custom">("readwrite");
  const [customPolicyText, setCustomPolicyText] = useState(READ_WRITE_POLICY);
  const [creatingKey, setCreatingKey] = useState(false);
  const [createdResult, setCreatedResult] = useState<CredentialItem | null>(null);

  // Result display copy states
  const [copiedAccess, setCopiedAccess] = useState(false);
  const [copiedSecret, setCopiedSecret] = useState(false);
  const [confirmedSave, setConfirmedSave] = useState(false);

  // Copy states for active table rows
  const [copiedRowKey, setCopiedRowKey] = useState<string | null>(null);

  // Policy view state
  const [viewingPolicy, setViewingPolicy] = useState<string | null>(null);

  // Revoke state
  const [revokingId, setRevokingId] = useState<string | null>(null);

  const fetchKeys = useCallback(async () => {
    try {
      const data = await listCredentials(bucket.ID);
      setCredentials(data || []);
    } catch (err: any) {
      toast.error(err.message || "Failed to load bucket access credentials");
    } finally {
      setLoading(false);
    }
  }, [bucket.ID]);

  useEffect(() => {
    void fetchKeys();
  }, [fetchKeys]);

  const copyToClipboard = (text: string, type: "access" | "secret" | "row", rowId?: string) => {
    navigator.clipboard.writeText(text);
    if (type === "access") {
      setCopiedAccess(true);
      setTimeout(() => setCopiedAccess(false), 1500);
    } else if (type === "secret") {
      setCopiedSecret(true);
      setTimeout(() => setCopiedSecret(false), 1500);
    } else if (type === "row" && rowId) {
      setCopiedRowKey(rowId);
      setTimeout(() => setCopiedRowKey(null), 1500);
    }
    toast.success("Copied to clipboard");
  };

  const handleGenerateKey = async (e: React.FormEvent) => {
    e.preventDefault();
    setCreatingKey(true);
    try {
      let finalPolicy = "";
      if (selectedPolicyTemplate === "readwrite") finalPolicy = READ_WRITE_POLICY;
      else if (selectedPolicyTemplate === "readonly") finalPolicy = READ_ONLY_POLICY;
      else {
        // Parse validation
        try {
          JSON.parse(customPolicyText);
          finalPolicy = customPolicyText;
        } catch {
          toast.error("Invalid custom policy JSON syntax");
          setCreatingKey(false);
          return;
        }
      }

      const res = await createCredential(bucket.ID, finalPolicy);
      setCreatedResult(res);
      setModalStep("result");
      toast.success("Access credential generated successfully");
    } catch (err: any) {
      toast.error(err.message || "Failed to generate credentials");
    } finally {
      setCreatingKey(false);
    }
  };

  const handleRevoke = async (id: string) => {
    if (!confirm("Are you sure you want to revoke this Access Key? All applications using it will lose access immediately.")) return;
    setRevokingId(id);
    try {
      await revokeCredential(id);
      toast.success("Access Key successfully revoked");
      await fetchKeys();
    } catch (err: any) {
      toast.error(err.message || "Failed to revoke credential");
    } finally {
      setRevokingId(null);
    }
  };

  const handleCloseModal = () => {
    setIsModalOpen(false);
    setModalStep("policy");
    setConfirmedSave(false);
    setCreatedResult(null);
    void fetchKeys();
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
                          onClick={() => copyToClipboard(cred.access_key, "row", cred.id)}
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
                        onClick={() => handleRevoke(cred.id)}
                        disabled={revokingId === cred.id}
                        className="h-7 px-2 hover:bg-red-500/10 text-red-655 hover:text-red-700 dark:hover:text-red-400 text-xs font-bold transition-all cursor-pointer rounded-md border border-transparent hover:border-red-500/20 disabled:opacity-50"
                      >
                        <Trash2 className="h-3.5 w-3.5 shrink-0" />
                        <span>Revoke</span>
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
      {isModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs">
          <div className="w-full max-w-lg bg-card text-card-foreground border border-border shadow-lg rounded-xl overflow-hidden animate-in fade-in zoom-in-95 duration-200">
            
            {/* Modal Header */}
            <div className="flex items-center justify-between px-5 py-4 border-b border-border">
              <div className="flex items-center gap-2">
                <div className="h-7 w-7 flex items-center justify-center rounded-lg bg-blue-600/10 text-blue-500 border border-blue-500/20">
                  <KeyRound className="h-4 w-4" />
                </div>
                <span className="font-bold text-sm text-foreground">
                  {modalStep === "policy" ? "Generate Key pair" : "Access Credentials Created"}
                </span>
              </div>
              {modalStep === "policy" && (
                <button
                  onClick={handleCloseModal}
                  className="text-muted-foreground/60 hover:text-foreground cursor-pointer outline-none"
                >
                  <X className="h-4 w-4" />
                </button>
              )}
            </div>

            {modalStep === "policy" ? (
              <form onSubmit={handleGenerateKey}>
                <div className="p-5 space-y-4">
                  {/* Select policy template */}
                  <div className="flex flex-col gap-1.5">
                    <label className="font-bold text-foreground">Select Access Policy</label>
                    <div className="grid grid-cols-3 gap-2">
                      <button
                        type="button"
                        onClick={() => setSelectedPolicyTemplate("readwrite")}
                        className={cn(
                          "py-2 px-3 text-center border rounded-md font-bold transition-all cursor-pointer",
                          selectedPolicyTemplate === "readwrite"
                            ? "bg-blue-500/10 border-blue-500/40 text-blue-600 dark:text-blue-400"
                            : "bg-background border-border text-muted-foreground hover:bg-muted"
                        )}
                      >
                        Read-Write
                      </button>
                      <button
                        type="button"
                        onClick={() => setSelectedPolicyTemplate("readonly")}
                        className={cn(
                          "py-2 px-3 text-center border rounded-md font-bold transition-all cursor-pointer",
                          selectedPolicyTemplate === "readonly"
                            ? "bg-blue-500/10 border-blue-500/40 text-blue-600 dark:text-blue-400"
                            : "bg-background border-border text-muted-foreground hover:bg-muted"
                        )}
                      >
                        Read-Only
                      </button>
                      <button
                        type="button"
                        onClick={() => setSelectedPolicyTemplate("custom")}
                        className={cn(
                          "py-2 px-3 text-center border rounded-md font-bold transition-all cursor-pointer",
                          selectedPolicyTemplate === "custom"
                            ? "bg-blue-500/10 border-blue-500/40 text-blue-600 dark:text-blue-400"
                            : "bg-background border-border text-muted-foreground hover:bg-muted"
                        )}
                      >
                        Custom JSON
                      </button>
                    </div>
                  </div>

                  {/* Custom policy JSON textarea */}
                  {selectedPolicyTemplate === "custom" ? (
                    <div className="flex flex-col gap-1.5">
                      <label className="font-bold text-foreground">Custom JSON Policy</label>
                      <textarea
                        rows={8}
                        value={customPolicyText}
                        onChange={(e) => setCustomPolicyText(e.target.value)}
                        placeholder="Input Policy JSON..."
                        required
                        className="w-full p-2.5 bg-slate-950 text-slate-100 rounded-md font-mono text-[11px] border border-border focus:outline-none focus:border-blue-500"
                      />
                    </div>
                  ) : (
                    <div className="flex flex-col gap-1.5">
                      <label className="font-bold text-muted-foreground">Access Policy JSON Preview</label>
                      <pre className="p-3 bg-slate-900 text-slate-100 rounded-md font-mono text-[10px] overflow-x-auto max-h-32">
                        {selectedPolicyTemplate === "readwrite" ? READ_WRITE_POLICY : READ_ONLY_POLICY}
                      </pre>
                    </div>
                  )}
                </div>

                {/* Actions */}
                <div className="flex items-center justify-end gap-2 px-5 py-3.5 bg-muted/20 border-t border-border">
                  <Button
                    type="button"
                    variant="outline"
                    onClick={handleCloseModal}
                    className="h-8.5 text-xs font-bold transition-colors cursor-pointer border-border text-foreground hover:bg-muted"
                  >
                    Cancel
                  </Button>
                  <Button
                    type="submit"
                    disabled={creatingKey}
                    className="h-8.5 text-xs font-bold bg-blue-600 hover:bg-blue-700 text-white rounded-md flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
                  >
                    {creatingKey && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                    <span>Generate Key Pair</span>
                  </Button>
                </div>
              </form>
            ) : (
              <div className="p-5 space-y-4.5 select-none">
                {/* Warning alerts */}
                <div className="rounded-lg border border-amber-500/25 bg-amber-500/5 p-4 text-amber-800 dark:text-amber-300 leading-relaxed flex gap-3">
                  <ShieldAlert className="h-5 w-5 shrink-0 text-amber-500" />
                  <div>
                    <p className="font-bold">Store this credentials pair safely!</p>
                    <p className="mt-1 text-[11px] font-medium opacity-90 leading-normal">
                      The **Secret Key** will only be shown this **one time**. If you close this window without saving it, you will have to generate a new key pair.
                    </p>
                  </div>
                </div>

                {/* Display Access/Secret keys */}
                <div className="space-y-3.5">
                  <div className="flex flex-col gap-1.5">
                    <span className="font-bold text-foreground">Access Key</span>
                    <div className="flex h-9 items-center justify-between border border-border pl-3 pr-1 bg-muted/10 rounded-md font-mono text-[11px] text-foreground">
                      <span className="truncate font-semibold select-all">{createdResult?.access_key}</span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        onClick={() => createdResult && copyToClipboard(createdResult.access_key, "access")}
                        className="h-7 w-7 text-muted-foreground hover:text-foreground cursor-pointer transition-colors"
                      >
                        {copiedAccess ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                      </Button>
                    </div>
                  </div>

                  <div className="flex flex-col gap-1.5">
                    <span className="font-bold text-foreground">Secret Key</span>
                    <div className="flex h-9 items-center justify-between border border-border pl-3 pr-1 bg-muted/10 rounded-md font-mono text-[11px] text-foreground">
                      <span className="truncate font-bold text-blue-600 dark:text-blue-400 select-all">{createdResult?.secret_key}</span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        onClick={() => createdResult && copyToClipboard(createdResult.secret_key || "", "secret")}
                        className="h-7 w-7 text-muted-foreground hover:text-foreground cursor-pointer transition-colors"
                      >
                        {copiedSecret ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
                      </Button>
                    </div>
                  </div>
                </div>

                {/* Confirm box */}
                <div className="pt-3 border-t border-border/80">
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

                {/* Finalize action */}
                <div className="flex items-center justify-end pt-1">
                  <Button
                    type="button"
                    disabled={!confirmedSave}
                    onClick={handleCloseModal}
                    className="h-8.5 px-6 text-xs font-bold bg-emerald-600 hover:bg-emerald-700 text-white rounded-md flex items-center gap-1.5 cursor-pointer disabled:opacity-40"
                  >
                    <CheckSquare className="h-4 w-4" />
                    <span>Save & Close</span>
                  </Button>
                </div>
              </div>
            )}

          </div>
        </div>
      )}

      {/* POLICY VIEW PANEL (INLINE DIALOG) */}
      {viewingPolicy && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs">
          <div className="w-full max-w-lg bg-card text-card-foreground border border-border shadow-lg rounded-xl overflow-hidden animate-in fade-in zoom-in-95 duration-200">
            <div className="flex items-center justify-between px-5 py-4 border-b border-border">
              <span className="font-bold text-sm text-foreground flex items-center gap-1.5">
                <FileCode className="h-4 w-4 text-blue-500" />
                <span>Access Policy JSON</span>
              </span>
              <button
                onClick={() => setViewingPolicy(null)}
                className="text-muted-foreground/60 hover:text-foreground cursor-pointer outline-none"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="p-5">
              <pre className="p-4 bg-slate-950 text-slate-100 rounded-md font-mono text-[11px] overflow-x-auto max-h-72">
                {viewingPolicy}
              </pre>
            </div>
            <div className="flex items-center justify-end px-5 py-3.5 bg-muted/20 border-t border-border">
              <Button
                type="button"
                onClick={() => setViewingPolicy(null)}
                className="h-8.5 text-xs font-bold bg-slate-800 hover:bg-slate-700 text-white rounded-md cursor-pointer"
              >
                Close
              </Button>
            </div>
          </div>
        </div>
      )}

    </div>
  );
}
