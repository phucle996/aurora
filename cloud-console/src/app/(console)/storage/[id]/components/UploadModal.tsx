"use client";

import { useState } from "react";
import { Loader2, Upload, X } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";

export function UploadModal({
  isOpen,
  prefix,
  onClose,
  onUpload,
  onUploaded,
}: {
  isOpen: boolean;
  prefix: string;
  onClose: () => void;
  onUpload: (file: File, objectKey: string) => Promise<void>;
  onUploaded: () => void;
}) {
  const [file, setFile] = useState<File | null>(null);
  const [objectKey, setObjectKey] = useState("");
  const [uploading, setUploading] = useState(false);

  if (!isOpen) return null;

  const closeModal = () => {
    if (uploading) return;
    setFile(null);
    setObjectKey("");
    onClose();
  };

  const chooseFile = (selected: File | undefined) => {
    if (!selected) return;
    setFile(selected);
    setObjectKey((current) => current || `${prefix}${selected.name}`);
  };

  const submit = async () => {
    const key = objectKey.trim();
    if (!file || !key || key.includes("\\") || key.split("/").some((part) => !part || part === "." || part === "..")) {
      toast.error("Choose a file and enter a valid object key.");
      return;
    }
    setUploading(true);
    try {
      await onUpload(file, key);
      toast.success(`Uploaded ${key}.`);
      onUploaded();
      setFile(null);
      setObjectKey("");
      onClose();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Unable to upload object.");
    } finally {
      setUploading(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-modal="true" aria-labelledby="storage-transfer-title">
      <div className="w-full max-w-md rounded-[8px] border border-border bg-card p-5 shadow-sm">
        <div className="flex items-start justify-between gap-3 border-b border-border pb-3">
          <div className="flex items-center gap-2"><Upload className="h-4 w-4 text-blue-500" /><h2 id="storage-transfer-title" className="text-sm font-semibold">Upload object</h2></div>
          <button type="button" onClick={closeModal} disabled={uploading} className="rounded-[4px] p-1 text-muted-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-blue-500" aria-label="Close upload dialog"><X className="h-4 w-4" /></button>
        </div>
        <div className="space-y-4 py-5">
          <div>
            <label htmlFor="storage-upload-file" className="mb-1.5 block text-xs font-semibold">File</label>
            <input id="storage-upload-file" type="file" onChange={(event) => chooseFile(event.target.files?.[0])} disabled={uploading} className="block w-full text-xs file:mr-3 file:rounded file:border-0 file:bg-muted file:px-3 file:py-1.5 file:text-xs" />
          </div>
          <div>
            <label htmlFor="storage-upload-key" className="mb-1.5 block text-xs font-semibold">Object key</label>
            <input id="storage-upload-key" value={objectKey} onChange={(event) => setObjectKey(event.target.value)} disabled={uploading} className="w-full rounded-[4px] border border-border bg-background px-3 py-2 text-xs outline-none focus-visible:ring-2 focus-visible:ring-blue-500" placeholder={`${prefix}filename.ext`} autoComplete="off" />
          </div>
          <p className="text-[11px] leading-relaxed text-muted-foreground">The browser receives a one-time Zone transfer ticket. No access key, secret key, or ticket is stored in browser storage.</p>
        </div>
        <div className="flex justify-end gap-2"><Button variant="outline" onClick={closeModal} disabled={uploading}>Cancel</Button><Button onClick={() => void submit()} disabled={!file || !objectKey.trim() || uploading}>{uploading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Upload className="h-3.5 w-3.5" />} Upload</Button></div>
      </div>
    </div>
  );
}
