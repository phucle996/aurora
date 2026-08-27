"use client";

import { useRef, useState } from "react";
import { Loader2, Upload, X } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { type MultipartProgress } from "@/features/storage/objects/api";

function formatSpeed(bytesPerSec?: number): string {
  if (!bytesPerSec || bytesPerSec <= 0) return "";
  if (bytesPerSec >= 1024 * 1024) {
    return `${(bytesPerSec / (1024 * 1024)).toFixed(1)} MB/s`;
  }
  return `${(bytesPerSec / 1024).toFixed(0)} KB/s`;
}

function formatSize(bytes: number): string {
  if (bytes >= 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${bytes} B`;
}

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
  onUpload: (
    file: File,
    objectKey: string,
    onProgress?: (progress: MultipartProgress) => void,
    signal?: AbortSignal,
  ) => Promise<void>;
  onUploaded: () => void;
}) {
  const [file, setFile] = useState<File | null>(null);
  const [objectKey, setObjectKey] = useState("");
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState<MultipartProgress | null>(null);
  const abortControllerRef = useRef<AbortController | null>(null);

  if (!isOpen) return null;

  const closeModal = () => {
    if (uploading) {
      if (confirm("Cancel ongoing upload?")) {
        abortControllerRef.current?.abort();
        setUploading(false);
        setProgress(null);
        setFile(null);
        setObjectKey("");
        onClose();
      }
      return;
    }
    setFile(null);
    setObjectKey("");
    setProgress(null);
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
    const abortController = new AbortController();
    abortControllerRef.current = abortController;
    setUploading(true);
    setProgress(null);

    try {
      await onUpload(file, key, (p) => setProgress(p), abortController.signal);
      toast.success(`Uploaded ${key}.`);
      onUploaded();
      setFile(null);
      setObjectKey("");
      setProgress(null);
      onClose();
    } catch (error) {
      if (abortController.signal.aborted) {
        toast.info("Upload cancelled.");
      } else {
        toast.error(error instanceof Error ? error.message : "Unable to upload object.");
      }
    } finally {
      setUploading(false);
      abortControllerRef.current = null;
    }
  };

  const percent = progress && progress.total > 0
    ? Math.min(100, Math.round((progress.loaded / progress.total) * 100))
    : null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-modal="true" aria-labelledby="storage-transfer-title">
      <div className="w-full max-w-md rounded-[8px] border border-border bg-card p-5 shadow-sm">
        <div className="flex items-start justify-between gap-3 border-b border-border pb-3">
          <div className="flex items-center gap-2">
            <Upload className="h-4 w-4 text-blue-500" />
            <h2 id="storage-transfer-title" className="text-sm font-semibold">Upload object</h2>
          </div>
          <button
            type="button"
            onClick={closeModal}
            className="rounded-[4px] p-1 text-muted-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-blue-500"
            aria-label="Close upload dialog"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <div className="space-y-4 py-5">
          <div>
            <label htmlFor="storage-upload-file" className="mb-1.5 block text-xs font-semibold">File</label>
            <input
              id="storage-upload-file"
              type="file"
              onChange={(event) => chooseFile(event.target.files?.[0])}
              disabled={uploading}
              className="block w-full text-xs file:mr-3 file:rounded file:border-0 file:bg-muted file:px-3 file:py-1.5 file:text-xs"
            />
            {file && (
              <div className="mt-1 flex items-center justify-between text-[11px] text-muted-foreground">
                <span>Size: {formatSize(file.size)}</span>
                {file.size >= 10 * 1024 * 1024 && (
                  <span className="font-medium text-blue-600 dark:text-blue-400">
                    S3 Multipart Chunked Upload ({Math.ceil(file.size / (10 * 1024 * 1024))} parts)
                  </span>
                )}
              </div>
            )}
          </div>

          <div>
            <label htmlFor="storage-upload-key" className="mb-1.5 block text-xs font-semibold">Object key</label>
            <input
              id="storage-upload-key"
              value={objectKey}
              onChange={(event) => setObjectKey(event.target.value)}
              disabled={uploading}
              className="w-full rounded-[4px] border border-border bg-background px-3 py-2 text-xs outline-none focus-visible:ring-2 focus-visible:ring-blue-500"
              placeholder={`${prefix}filename.ext`}
              autoComplete="off"
            />
          </div>

          {uploading && percent !== null && progress && (
            <div className="space-y-2 rounded-[6px] border border-border/70 bg-muted/40 p-3">
              <div className="flex items-center justify-between text-xs">
                <span className="font-medium">
                  {progress.partIndex !== undefined && progress.totalParts > 1
                    ? `Uploading Part ${progress.partIndex}/${progress.totalParts}...`
                    : "Uploading..."}
                </span>
                <span className="font-semibold text-blue-600 dark:text-blue-400">{percent}%</span>
              </div>
              <div className="h-2 w-full overflow-hidden rounded-full bg-secondary">
                <div
                  className="h-full bg-blue-600 transition-all duration-300 dark:bg-blue-500"
                  style={{ width: `${percent}%` }}
                />
              </div>
              <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                <span>{formatSize(progress.loaded)} of {formatSize(progress.total)}</span>
                {progress.speedBytesPerSec ? <span>{formatSpeed(progress.speedBytesPerSec)}</span> : null}
              </div>
            </div>
          )}

          <p className="text-[11px] leading-relaxed text-muted-foreground">
            The browser receives one-time Zone transfer tickets. No access key, secret key, or ticket is stored in browser storage.
          </p>
        </div>

        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={closeModal}>
            {uploading ? "Cancel" : "Close"}
          </Button>
          <Button onClick={() => void submit()} disabled={!file || !objectKey.trim() || uploading}>
            {uploading ? (
              <>
                <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                {percent !== null ? `${percent}%` : "Uploading..."}
              </>
            ) : (
              <>
                <Upload className="mr-1.5 h-3.5 w-3.5" />
                Upload
              </>
            )}
          </Button>
        </div>
      </div>
    </div>
  );
}
