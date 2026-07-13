"use client";

import React, { useState, useEffect } from "react";
import { KeyRound, X, Loader2, ShieldAlert, Check, Copy, CheckSquare } from "lucide-react";
import { Button } from "@/components/ui/button";
import { type CredentialItem } from "@/lib/api/storage";
import { cn } from "@/lib/utils";
import { toast } from "sonner";

interface GenerateCredentialModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSubmit: (policy: string) => void;
  isPending: boolean;
  createdResult: CredentialItem | null;
  bucketName: string;
}

const getReadWritePolicy = (bucketName: string) => `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:*"
      ],
      "Resource": [
        "arn:aws:s3:::${bucketName}",
        "arn:aws:s3:::${bucketName}/*"
      ]
    }
  ]
}`;

const getReadOnlyPolicy = (bucketName: string) => `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:Get*",
        "s3:List*"
      ],
      "Resource": [
        "arn:aws:s3:::${bucketName}",
        "arn:aws:s3:::${bucketName}/*"
      ]
    }
  ]
}`;

const validateClientPolicy = (policyJsonStr: string, bucketName: string): boolean => {
  try {
    const policy = JSON.parse(policyJsonStr);
    if (!policy.Statement || !Array.isArray(policy.Statement)) return false;

    const allowedPrefix = `arn:aws:s3:::${bucketName}`;

    for (const stmt of policy.Statement) {
      if (stmt.Effect === "Allow" && stmt.Resource) {
        const resources = Array.isArray(stmt.Resource) ? stmt.Resource : [stmt.Resource];
        for (const res of resources) {
          if (typeof res !== "string") return false;
          // Must exactly match the bucket ARN or start with the bucket ARN followed by a slash
          const isValid = res === allowedPrefix || res.startsWith(`${allowedPrefix}/`);
          if (!isValid) return false;
        }
      }
    }
    return true;
  } catch {
    return false;
  }
};

export function GenerateCredentialModal({
  isOpen,
  onClose,
  onSubmit,
  isPending,
  createdResult,
  bucketName,
}: GenerateCredentialModalProps) {
  const [modalStep, setModalStep] = useState<"policy" | "result">("policy");
  const [selectedPolicyTemplate, setSelectedPolicyTemplate] = useState<"readwrite" | "readonly" | "custom">("readwrite");
  const [customPolicyText, setCustomPolicyText] = useState("");

  const [copiedAccess, setCopiedAccess] = useState(false);
  const [copiedSecret, setCopiedSecret] = useState(false);
  const [confirmedSave, setConfirmedSave] = useState(false);

  // Sync modal steps and reset policy text when opened/closed or bucketName changes
  useEffect(() => {
    if (isOpen) {
      setModalStep("policy");
      setConfirmedSave(false);
      setCustomPolicyText(getReadWritePolicy(bucketName));
    }
  }, [isOpen, bucketName]);

  useEffect(() => {
    if (createdResult) {
      setModalStep("result");
    }
  }, [createdResult]);

  if (!isOpen) return null;

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

  const handleSubmitForm = (e: React.FormEvent) => {
    e.preventDefault();
    let finalPolicy = "";
    if (selectedPolicyTemplate === "readwrite") {
      finalPolicy = getReadWritePolicy(bucketName);
    } else if (selectedPolicyTemplate === "readonly") {
      finalPolicy = getReadOnlyPolicy(bucketName);
    } else {
      try {
        JSON.parse(customPolicyText);
        finalPolicy = customPolicyText;
      } catch {
        toast.error("Invalid custom policy JSON syntax");
        return;
      }
      
      // Perform security check on custom policy
      if (!validateClientPolicy(finalPolicy, bucketName)) {
        toast.error(`Security Violation: Policy statements must only target bucket: arn:aws:s3:::${bucketName}`);
        return;
      }
    }
    onSubmit(finalPolicy);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs p-4">
      {/* Container is max-w-2xl (approx 30% wider than original max-w-lg) and max-h-[90vh] with auto height */}
      <div className="w-full max-w-2xl bg-card text-card-foreground border border-border shadow-lg rounded-xl overflow-hidden animate-in fade-in zoom-in-95 duration-200 flex flex-col max-h-[90vh]">
        {/* Modal Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border shrink-0">
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
              onClick={onClose}
              className="text-muted-foreground/60 hover:text-foreground cursor-pointer outline-none"
            >
              <X className="h-4 w-4" />
            </button>
          )}
        </div>

        {/* Scrollable content container with auto height */}
        <div className="flex-1 overflow-y-auto">
          {modalStep === "policy" ? (
            <form onSubmit={handleSubmitForm} className="flex flex-col h-full">
              <div className="p-5 space-y-4 flex-1">
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

                {/* Custom policy JSON textarea - height is auto adjusted, no scroll */}
                {selectedPolicyTemplate === "custom" ? (
                  <div className="flex flex-col gap-1.5">
                    <label className="font-bold text-foreground">Custom JSON Policy</label>
                    <textarea
                      rows={14}
                      value={customPolicyText}
                      onChange={(e) => setCustomPolicyText(e.target.value)}
                      placeholder="Input Policy JSON..."
                      required
                      className="w-full p-2.5 bg-slate-950 text-slate-100 rounded-md font-mono text-[11px] border border-border focus:outline-none focus:border-blue-500 h-auto min-h-[250px]"
                    />
                  </div>
                ) : (
                  <div className="flex flex-col gap-1.5">
                    <label className="font-bold text-muted-foreground">Access Policy JSON Preview</label>
                    <pre className="p-3 bg-slate-900 text-slate-100 rounded-md font-mono text-[10px] overflow-x-auto max-h-[300px] overflow-y-auto">
                      {selectedPolicyTemplate === "readwrite" ? getReadWritePolicy(bucketName) : getReadOnlyPolicy(bucketName)}
                    </pre>
                  </div>
                )}
              </div>

              {/* Actions */}
              <div className="flex items-center justify-end gap-2 px-5 py-3.5 bg-muted/20 border-t border-border shrink-0">
                <Button
                  type="button"
                  variant="outline"
                  onClick={onClose}
                  className="h-8.5 text-xs font-bold transition-colors cursor-pointer border-border text-foreground hover:bg-muted"
                >
                  Cancel
                </Button>
                <Button
                  type="submit"
                  disabled={isPending}
                  className="h-8.5 text-xs font-bold bg-blue-600 hover:bg-blue-700 text-white rounded-md flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
                >
                  {isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
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
                  onClick={onClose}
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
    </div>
  );
}
