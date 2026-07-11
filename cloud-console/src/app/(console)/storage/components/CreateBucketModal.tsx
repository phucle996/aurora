import React, { useState } from "react";
import { X, Copy, Check, ShieldAlert, KeyRound, CheckSquare, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";
import { createBucket, type CreatedBucketResult } from "@/lib/api/storage";
import { type ZoneCatalogItem } from "@/lib/api/zone";

interface CreateBucketModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess: () => void;
  zones: ZoneCatalogItem[];
}

export function CreateBucketModal({
  isOpen,
  onClose,
  onSuccess,
  zones,
}: CreateBucketModalProps) {
  const [step, setStep] = useState<"form" | "result">("form");
  const [loading, setLoading] = useState(false);

  // Form states
  const [name, setName] = useState("");
  const [quotaGB, setQuotaGB] = useState<number>(50);
  const [zoneID, setZoneID] = useState("");

  // Result state
  const [result, setResult] = useState<CreatedBucketResult | null>(null);
  const [copiedAccess, setCopiedAccess] = useState(false);
  const [copiedSecret, setCopiedSecret] = useState(false);
  const [confirmedSave, setConfirmedSave] = useState(false);

  // Initialize zone selection
  React.useEffect(() => {
    if (zones.length > 0 && !zoneID) {
      setZoneID(zones[0].id);
    }
  }, [zones, zoneID]);

  if (!isOpen) return null;

  const handleNameChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    // Standard bucket naming rules: lowercase, numbers, hyphens, and dots only
    const value = e.target.value.toLowerCase().replace(/[^a-z0-9.-]/g, "");
    setName(value);
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      toast.error("Bucket name is required");
      return;
    }
    if (!zoneID) {
      toast.error("Zone selection is required");
      return;
    }
    if (quotaGB <= 0) {
      toast.error("Quota must be greater than 0 GB");
      return;
    }

    setLoading(true);
    try {
      const quotaBytes = quotaGB * 1024 * 1024 * 1024;
      const res = await createBucket(name, quotaBytes, zoneID);
      setResult(res);
      setStep("result");
      toast.success("Bucket created successfully");
    } catch (err: any) {
      toast.error(err.message || "Failed to create storage bucket");
    } finally {
      setLoading(false);
    }
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
    onSuccess();
    // Reset states
    setStep("form");
    setName("");
    setQuotaGB(50);
    setConfirmedSave(false);
    setResult(null);
    onClose();
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs select-none">
      <div className="w-full max-w-lg bg-card text-card-foreground border border-border shadow-lg rounded-xl overflow-hidden animate-in fade-in zoom-in-95 duration-200">
        
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border">
          <div className="flex items-center gap-2">
            <div className="h-7 w-7 flex items-center justify-center rounded-lg bg-blue-600/10 text-blue-500 border border-blue-500/20">
              <KeyRound className="h-4 w-4" />
            </div>
            <span className="font-bold text-sm text-foreground select-none">
              {step === "form" ? "Create New Bucket" : "Bucket Credentials Created"}
            </span>
          </div>
          {step === "form" && (
            <button
              onClick={onClose}
              className="text-muted-foreground/60 hover:text-foreground cursor-pointer outline-none"
            >
              <X className="h-4 w-4" />
            </button>
          )}
        </div>

        {step === "form" ? (
          <form onSubmit={handleSubmit}>
            {/* Form Fields */}
            <div className="p-5 space-y-4 text-xs">
              
              {/* Name */}
              <div className="flex flex-col gap-1.5">
                <label className="font-bold text-foreground select-none">
                  Bucket Name
                </label>
                <input
                  type="text"
                  placeholder="e.g. user-uploads-bucket"
                  value={name}
                  onChange={handleNameChange}
                  required
                  maxLength={63}
                  className="w-full h-9 px-3 bg-background border border-border rounded-md focus:outline-none focus:border-blue-500 text-foreground placeholder:text-muted-foreground/40 transition-colors"
                />
                <span className="text-[10px] text-muted-foreground leading-normal mt-0.5">
                  Must be unique system-wide. Lowercase letters, numbers, hyphens, and dots only. Length 3-63.
                </span>
              </div>

              {/* Zone */}
              <div className="flex flex-col gap-1.5">
                <label className="font-bold text-foreground select-none">
                  Infrastructure Zone
                </label>
                <select
                  value={zoneID}
                  onChange={(e) => setZoneID(e.target.value)}
                  className="w-full h-9 px-2 bg-background border border-border rounded-md focus:outline-none focus:border-blue-500 text-foreground cursor-pointer transition-colors"
                >
                  {zones.map((z) => (
                    <option key={z.id} value={z.id}>
                      {z.name} ({z.code})
                    </option>
                  ))}
                </select>
                <span className="text-[10px] text-muted-foreground leading-normal mt-0.5">
                  Choose the cluster zone where bucket files will reside physically.
                </span>
              </div>

              {/* Quota */}
              <div className="flex flex-col gap-1.5">
                <label className="font-bold text-foreground select-none">
                  Capacity Limit (GB)
                </label>
                <input
                  type="number"
                  min={1}
                  max={100000}
                  value={quotaGB}
                  onChange={(e) => setQuotaGB(parseInt(e.target.value) || 0)}
                  required
                  className="w-full h-9 px-3 bg-background border border-border rounded-md focus:outline-none focus:border-blue-500 text-foreground transition-colors"
                />
                <span className="text-[10px] text-muted-foreground leading-normal mt-0.5">
                  Storage space limit allocated for this bucket in Gigabytes.
                </span>
              </div>

            </div>

            {/* Footer Actions */}
            <div className="flex items-center justify-end gap-2 px-5 py-3.5 bg-muted/20 border-t border-border">
              <Button
                type="button"
                variant="outline"
                onClick={onClose}
                disabled={loading}
                className="h-8.5 text-xs font-bold transition-colors cursor-pointer border-border text-foreground hover:bg-muted"
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={loading || !name.trim()}
                className="h-8.5 text-xs font-bold bg-blue-600 hover:bg-blue-700 text-white rounded-md flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
              >
                {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                <span>Create Bucket</span>
              </Button>
            </div>
          </form>
        ) : (
          <div className="p-5 space-y-4.5 text-xs select-none">
            {/* Warning Alert banner */}
            <div className="rounded-lg border border-amber-500/25 bg-amber-500/5 p-4 text-amber-800 dark:text-amber-300 leading-relaxed flex gap-3">
              <ShieldAlert className="h-5 w-5 shrink-0 text-amber-500" />
              <div>
                <p className="font-bold">Store these credentials safely!</p>
                <p className="mt-1 text-[11px] font-medium opacity-90 leading-normal">
                  The **Secret Key** will only be shown this **one time**. If you close this window without saving it, you will have to generate a new key pair or rebuild the bucket.
                </p>
              </div>
            </div>

            {/* Credentials Fields */}
            <div className="space-y-3.5">
              
              {/* Bucket ID */}
              <div className="flex flex-col gap-1.5">
                <span className="font-bold text-foreground">Bucket ID</span>
                <div className="flex h-9 items-center justify-between border border-border px-3 bg-muted/40 rounded-md font-mono text-[11px] text-slate-600 dark:text-slate-400">
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
                <pre className="p-3 bg-slate-900 text-slate-100 rounded-md font-mono text-[10px] overflow-x-auto max-h-24">
                  {result?.policy}
                </pre>
              </div>

            </div>

            {/* Checkbox confirmation */}
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

            {/* Close action */}
            <div className="flex items-center justify-end pt-1">
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
    </div>
  );
}
