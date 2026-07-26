import React, { useState } from "react";
import { ShieldAlert, Download, Check, Copy, CheckSquare } from "lucide-react";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";
import { type CreatedBucketResult } from "@/features/storage/api";

interface CreatedBucketResultViewProps {
  result: CreatedBucketResult;
  onFinalize: () => void;
}

export function CreatedBucketResultView({ result, onFinalize }: CreatedBucketResultViewProps) {
  const [copiedAccess, setCopiedAccess] = useState(false);
  const [copiedSecret, setCopiedSecret] = useState(false);
  const [confirmedSave, setConfirmedSave] = useState(false);

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

  // [COMMENT]: Hàm hỗ trợ tải file JSON chứa toàn bộ thông tin credential vừa tạo để lưu trữ an toàn
  const downloadJSON = () => {
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

  return (
    <div className="bg-card text-card-foreground border border-border rounded-lg shadow-xs p-6 space-y-5 text-xs select-none self-start">
      {/* Warning Alert banner */}
      <div className="rounded-lg border border-amber-505/25 bg-amber-500/5 p-4 text-amber-800 dark:text-amber-300 leading-relaxed flex gap-3">
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
          <div className="flex h-9 items-center justify-between border border-border px-3 bg-muted/40 rounded-md font-mono text-[11px] text-slate-500 dark:text-slate-400">
            <span className="truncate">{result.bucket_id}</span>
          </div>
        </div>

        {/* Access Key */}
        <div className="flex flex-col gap-1.5">
          <span className="font-bold text-foreground">Access Key</span>
          <div className="flex h-9 items-center justify-between border border-border pl-3 pr-1 bg-muted/10 rounded-md font-mono text-[11px] text-foreground">
            <span className="truncate font-semibold select-all">{result.access_key}</span>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={() => copyToClipboard(result.access_key, "access")}
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
            <span className="truncate font-bold text-blue-600 dark:text-blue-400 select-all">{result.secret_key}</span>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              onClick={() => copyToClipboard(result.secret_key, "secret")}
              className="h-7 w-7 text-muted-foreground hover:text-foreground cursor-pointer transition-colors"
            >
              {copiedSecret ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
            </Button>
          </div>
        </div>

        {/* JSON Policy */}
        <div className="flex flex-col gap-1.5">
          <span className="font-bold text-foreground">Default Access Policy</span>
          <pre className="p-3.5 bg-slate-900 dark:bg-slate-950 text-slate-100 dark:text-slate-200 rounded-md font-mono text-[10px] overflow-x-auto border border-slate-800 leading-normal select-text">
            {result.policy}
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
          onClick={onFinalize}
          className="h-8.5 px-6 text-xs font-bold bg-emerald-600 hover:bg-emerald-700 text-white rounded-md flex items-center gap-1.5 cursor-pointer disabled:opacity-40"
        >
          <CheckSquare className="h-4 w-4" />
          <span>Save & Close</span>
        </Button>
      </div>
    </div>
  );
}
