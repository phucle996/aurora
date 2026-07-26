import React, { useState } from "react";
import {
  Loader2,
  X,
  Check,
  ShieldAlert,
  ChevronDown,
  ChevronUp,
  Tag,
  Plus,
  History,
  Shield,
  Lock,
  Scale,
  Settings,
} from "lucide-react";
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

  // Advanced Options
  encryptEnabled: boolean;
  setEncryptEnabled: (val: boolean) => void;
  versioningEnabled: boolean;
  setVersioningEnabled: (val: boolean) => void;
  objectLockingEnabled: boolean;
  setObjectLockingEnabled: (val: boolean) => void;
  replicationEnabled: boolean;
  setReplicationEnabled: (val: boolean) => void;
  retentionDays: number;
  setRetentionDays: (val: number) => void;
  legalHoldEnabled: boolean;
  setLegalHoldEnabled: (val: boolean) => void;
  tags: Record<string, string>;
  setTags: React.Dispatch<React.SetStateAction<Record<string, string>>>;
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

  encryptEnabled,
  setEncryptEnabled,
  versioningEnabled,
  setVersioningEnabled,
  objectLockingEnabled,
  setObjectLockingEnabled,
  replicationEnabled,
  setReplicationEnabled,
  retentionDays,
  setRetentionDays,
  legalHoldEnabled,
  setLegalHoldEnabled,
  tags,
  setTags,
}: CreateBucketFormProps) {
  const textareaRef = React.useRef<HTMLTextAreaElement>(null);
  const [showAdvancedStorage, setShowAdvancedStorage] = useState(false);
  const [tagKey, setTagKey] = useState("");
  const [tagVal, setTagVal] = useState("");

  React.useEffect(() => {
    const textarea = textareaRef.current;
    if (textarea && selectedPolicyTemplate === "custom") {
      textarea.style.height = "auto";
      textarea.style.height = `${textarea.scrollHeight}px`;
    }
  }, [customPolicyText, selectedPolicyTemplate]);

  const handleNameChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e.target.value.toLowerCase().replace(/[^a-z0-9.-]/g, "");
    setName(value);
  };

  const handleAddTag = (e: React.MouseEvent) => {
    e.preventDefault();
    if (!tagKey.trim() || !tagVal.trim()) return;
    setTags((prev) => ({
      ...prev,
      [tagKey.trim()]: tagVal.trim(),
    }));
    setTagKey("");
    setTagVal("");
  };

  const handleRemoveTag = (keyToRemove: string) => {
    setTags((prev) => {
      const copy = { ...prev };
      delete copy[keyToRemove];
      return copy;
    });
  };

  const handleObjectLockingToggle = (val: boolean) => {
    setObjectLockingEnabled(val);
    if (val) {
      // Object Locking requires Versioning to be enabled
      setVersioningEnabled(true);
    }
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

        {/* SECTION 1.5: Advanced Storage Configurations */}
        <div className="border-b border-border/60 pb-5">
          <button
            type="button"
            onClick={() => setShowAdvancedStorage(!showAdvancedStorage)}
            className="flex items-center justify-between w-full text-[11px] font-bold text-foreground uppercase tracking-wider mb-2 select-none hover:text-blue-500 cursor-pointer outline-none"
          >
            <div className="flex items-center gap-2">
              <Settings size={14} className="text-slate-400" />
              <span>Advanced Storage Options</span>
            </div>
            {showAdvancedStorage ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
          </button>

          {showAdvancedStorage && (
            <div className="mt-4 space-y-5 animate-in fade-in slide-in-from-top duration-200">
              
              {/* Encryption & Versioning */}
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {/* SSE Encryption */}
                <div className="p-3 bg-slate-50 dark:bg-slate-900/30 border border-slate-100 dark:border-slate-800 rounded-lg flex items-start justify-between gap-4">
                  <div className="space-y-1">
                    <div className="flex items-center gap-1.5 font-bold text-foreground">
                      <Shield size={14} className="text-blue-500" />
                      <span>Server-Side Encryption</span>
                    </div>
                    <p className="text-[10px] text-muted-foreground leading-normal">
                      Encrypts your object data at rest using standard SSE-S3 management.
                    </p>
                  </div>
                  <input
                    type="checkbox"
                    checked={encryptEnabled}
                    onChange={(e) => setEncryptEnabled(e.target.checked)}
                    disabled={loading}
                    className="h-4.5 w-4.5 rounded border-slate-300 dark:border-slate-800 text-blue-600 focus:ring-blue-500 cursor-pointer"
                  />
                </div>

                {/* Object Versioning */}
                <div className="p-3 bg-slate-50 dark:bg-slate-900/30 border border-slate-100 dark:border-slate-800 rounded-lg flex items-start justify-between gap-4">
                  <div className="space-y-1">
                    <div className="flex items-center gap-1.5 font-bold text-foreground">
                      <History size={14} className="text-emerald-500" />
                      <span>Object Versioning</span>
                    </div>
                    <p className="text-[10px] text-muted-foreground leading-normal">
                      Keep multiple variants of an object in the same bucket to protect against accidental deletes.
                    </p>
                  </div>
                  <input
                    type="checkbox"
                    checked={versioningEnabled}
                    onChange={(e) => {
                      setVersioningEnabled(e.target.checked);
                      if (!e.target.checked) {
                        setObjectLockingEnabled(false);
                      }
                    }}
                    disabled={loading}
                    className="h-4.5 w-4.5 rounded border-slate-300 dark:border-slate-800 text-blue-600 focus:ring-blue-500 cursor-pointer"
                  />
                </div>
              </div>

              {/* Object Locking & Replication */}
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                {/* Object Locking */}
                <div className="p-3 bg-slate-50 dark:bg-slate-900/30 border border-slate-100 dark:border-slate-800 rounded-lg flex items-start justify-between gap-4">
                  <div className="space-y-1">
                    <div className="flex items-center gap-1.5 font-bold text-foreground">
                      <Lock size={14} className="text-amber-500" />
                      <span>Object Locking</span>
                    </div>
                    <p className="text-[10px] text-muted-foreground leading-normal">
                      Write-Once-Read-Many (WORM) pattern to prevent object deletion or overwrite (requires versioning).
                    </p>
                  </div>
                  <input
                    type="checkbox"
                    checked={objectLockingEnabled}
                    onChange={(e) => handleObjectLockingToggle(e.target.checked)}
                    disabled={loading}
                    className="h-4.5 w-4.5 rounded border-slate-300 dark:border-slate-800 text-blue-600 focus:ring-blue-500 cursor-pointer"
                  />
                </div>

                {/* Replication */}
                <div className="p-3 bg-slate-50 dark:bg-slate-900/30 border border-slate-100 dark:border-slate-800 rounded-lg flex items-start justify-between gap-4">
                  <div className="space-y-1">
                    <div className="flex items-center gap-1.5 font-bold text-foreground">
                      <ShieldAlert size={14} className="text-rose-500" />
                      <span>Cross-Region Replication</span>
                    </div>
                    <p className="text-[10px] text-muted-foreground leading-normal">
                      Automatically replicate uploaded objects across different storage nodes/zones.
                    </p>
                  </div>
                  <input
                    type="checkbox"
                    checked={replicationEnabled}
                    onChange={(e) => setReplicationEnabled(e.target.checked)}
                    disabled={loading}
                    className="h-4.5 w-4.5 rounded border-slate-300 dark:border-slate-800 text-blue-600 focus:ring-blue-500 cursor-pointer"
                  />
                </div>
              </div>

              {/* Object Locking Options (Conditional) */}
              {objectLockingEnabled && (
                <div className="p-4 bg-amber-500/5 border border-amber-500/20 rounded-lg space-y-4 animate-in slide-in-from-top duration-200">
                  <span className="text-[10px] font-bold text-amber-600 uppercase tracking-wider block">
                    Object Lock Settings (WORM configuration)
                  </span>

                  <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                    {/* Retention Period */}
                    <div className="flex flex-col gap-1.5">
                      <label className="font-bold text-foreground select-none">
                        Default Retention Days
                      </label>
                      <input
                        type="number"
                        min={0}
                        max={3650}
                        value={retentionDays}
                        onChange={(e) => setRetentionDays(parseInt(e.target.value) || 0)}
                        disabled={loading}
                        className="w-full h-8.5 px-3 bg-background border border-border rounded-md focus:outline-none focus:border-blue-500 text-foreground transition-colors"
                      />
                      <p className="text-[9px] text-muted-foreground">
                        Lock objects for a default number of days (set 0 to disable default retention).
                      </p>
                    </div>

                    {/* Legal Hold */}
                    <div className="flex items-start justify-between gap-4 p-2 bg-background border border-border rounded-md">
                      <div className="space-y-1">
                        <div className="flex items-center gap-1.5 font-bold text-foreground">
                          <Scale size={13} className="text-amber-500" />
                          <span>Legal Hold</span>
                        </div>
                        <p className="text-[9px] text-muted-foreground leading-normal">
                          Indefinitely prevent object version deletion until explicitly disabled.
                        </p>
                      </div>
                      <input
                        type="checkbox"
                        checked={legalHoldEnabled}
                        onChange={(e) => setLegalHoldEnabled(e.target.checked)}
                        disabled={loading}
                        className="h-4 w-4 rounded border-slate-300 dark:border-slate-800 text-blue-600 focus:ring-blue-500 cursor-pointer"
                      />
                    </div>
                  </div>
                </div>
              )}

              {/* Bucket Tags */}
              <div className="p-4 bg-slate-50 dark:bg-slate-900/30 border border-slate-100 dark:border-slate-800 rounded-lg space-y-3">
                <div className="flex items-center gap-1.5 font-bold text-foreground">
                  <Tag size={14} className="text-indigo-500" />
                  <span>Bucket Tags</span>
                </div>
                <p className="text-[10px] text-muted-foreground leading-normal">
                  Add tags to categorize and organize your cloud resources.
                </p>

                {/* Form to add tags */}
                <div className="flex gap-2 max-w-md">
                  <input
                    type="text"
                    placeholder="Key"
                    value={tagKey}
                    onChange={(e) => setTagKey(e.target.value)}
                    disabled={loading}
                    className="flex-1 h-8 px-2 bg-background border border-border rounded text-[11px] focus:outline-none"
                  />
                  <input
                    type="text"
                    placeholder="Value"
                    value={tagVal}
                    onChange={(e) => setTagVal(e.target.value)}
                    disabled={loading}
                    className="flex-1 h-8 px-2 bg-background border border-border rounded text-[11px] focus:outline-none"
                  />
                  <button
                    type="button"
                    onClick={handleAddTag}
                    disabled={loading || !tagKey.trim() || !tagVal.trim()}
                    className="h-8 px-3 rounded bg-blue-600 hover:bg-blue-700 text-white font-bold cursor-pointer disabled:opacity-50 flex items-center gap-1 text-[11px]"
                  >
                    <Plus size={12} />
                    <span>Add</span>
                  </button>
                </div>

                {/* List tags */}
                {Object.keys(tags).length > 0 && (
                  <div className="flex flex-wrap gap-1.5 pt-1.5">
                    {Object.entries(tags).map(([key, val]) => (
                      <span
                        key={key}
                        onClick={() => handleRemoveTag(key)}
                        className="inline-flex items-center gap-1 px-2.5 py-0.5 rounded bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400 border border-blue-100/60 dark:border-blue-900/40 text-[10px] font-bold cursor-pointer group hover:bg-rose-50 hover:text-rose-600 hover:border-rose-200 transition-colors"
                        title="Click to remove tag"
                      >
                        <span>{key}={val}</span>
                        <X size={10} className="opacity-60 group-hover:opacity-100" />
                      </span>
                    ))}
                  </div>
                )}
              </div>

            </div>
          )}
        </div>

        {/* SECTION 2: Advanced (Access Policy) */}
        <div className="space-y-4">
          <span className="text-[11px] font-bold text-foreground uppercase tracking-wider block">
            Access Policy Configuration
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
