import React from "react";
import { Loader2, X, Check, ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

interface CreateBucketFormProps {
  name: string;
  setName: (val: string) => void;
  quotaGB: number;
  setQuotaGB: (val: number) => void;
  selectedPolicyTemplate: "readwrite" | "readonly" | "custom";
  setSelectedPolicyTemplate: (val: "readwrite" | "readonly" | "custom") => void;
  customPolicyText: string;
  setCustomPolicyText: (val: string) => void;
  loading: boolean;
  isNamesLoading: boolean;
  isDuplicateName: boolean;
  onSubmit: (e: React.FormEvent) => void;
  onCancel: () => void;
  getReadWritePolicy: () => string;
  getReadOnlyPolicy: () => string;
}

export function CreateBucketForm({
  name,
  setName,
  quotaGB,
  setQuotaGB,
  selectedPolicyTemplate,
  setSelectedPolicyTemplate,
  customPolicyText,
  setCustomPolicyText,
  loading,
  isNamesLoading,
  isDuplicateName,
  onSubmit,
  onCancel,
  getReadWritePolicy,
  getReadOnlyPolicy,
}: CreateBucketFormProps) {
  const textareaRef = React.useRef<HTMLTextAreaElement>(null);

  React.useEffect(() => {
    const textarea = textareaRef.current;
    if (textarea && selectedPolicyTemplate === "custom") {
      textarea.style.height = "auto";
      textarea.style.height = `${textarea.scrollHeight}px`;
    }
  }, [customPolicyText, selectedPolicyTemplate]);

  const handleNameChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    // Chỉ chấp nhận chữ thường, số, dấu gạch ngang và dấu chấm
    const value = e.target.value.toLowerCase().replace(/[^a-z0-9.-]/g, "");
    setName(value);
  };

  return (
    <form onSubmit={onSubmit} className="bg-card text-card-foreground border border-border rounded-lg shadow-xs overflow-hidden self-start">
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
        <div className="space-y-4">
          <span className="text-[11px] font-bold text-foreground uppercase tracking-wider block">
            Advanced Settings (Default Credentials Access Policy)
          </span>

          <div className="flex flex-col gap-2">
            <label className="font-bold text-foreground select-none">Select Access Policy</label>
            <div className="grid grid-cols-3 gap-2">
              <button
                type="button"
                onClick={() => setSelectedPolicyTemplate("readwrite")}
                disabled={loading}
                className={cn(
                  "py-2 px-3 text-center border rounded-md font-bold transition-all cursor-pointer text-xs",
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
                disabled={loading}
                className={cn(
                  "py-2 px-3 text-center border rounded-md font-bold transition-all cursor-pointer text-xs",
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
                disabled={loading}
                className={cn(
                  "py-2 px-3 text-center border rounded-md font-bold transition-all cursor-pointer text-xs",
                  selectedPolicyTemplate === "custom"
                    ? "bg-blue-500/10 border-blue-500/40 text-blue-600 dark:text-blue-400"
                    : "bg-background border-border text-muted-foreground hover:bg-muted"
                )}
              >
                Custom JSON
              </button>
            </div>
          </div>

          {selectedPolicyTemplate === "custom" ? (
            <div className="flex flex-col gap-1.5">
              <label className="font-bold text-foreground select-none">Custom JSON Policy</label>
              <textarea
                ref={textareaRef}
                value={customPolicyText}
                onChange={(e) => setCustomPolicyText(e.target.value)}
                placeholder="Input Custom Policy JSON..."
                required
                disabled={loading}
                className="w-full p-2.5 bg-slate-950 text-slate-100 rounded-md font-mono text-[11px] border border-border focus:outline-none focus:border-blue-500 h-auto min-h-[200px] resize-none overflow-hidden"
              />
            </div>
          ) : (
            <div className="flex flex-col gap-1.5">
              <label className="font-bold text-muted-foreground select-none">Access Policy JSON Preview</label>
              <pre className="p-3 bg-slate-900 text-slate-100 rounded-md font-mono text-[10px] overflow-x-auto max-h-[250px] overflow-y-auto leading-normal">
                {selectedPolicyTemplate === "readwrite" ? getReadWritePolicy() : getReadOnlyPolicy()}
              </pre>
            </div>
          )}

          <span className="text-[10px] text-muted-foreground leading-normal block select-none">
            Use placeholder <code className="bg-slate-100 dark:bg-slate-800 px-1 py-0.5 rounded font-mono font-bold text-blue-500">&lt;BUCKET_NAME&gt;</code> to automatically represent the final physical name of the bucket on MinIO.
          </span>
        </div>
      </div>

      {/* Submit & Cancel Actions */}
      <div className="flex items-center justify-end gap-2 px-6 py-4 bg-muted/10 border-t border-border">
        <Button
          type="button"
          variant="outline"
          onClick={onCancel}
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
  );
}
