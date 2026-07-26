"use client";

import { AlertTriangle, X } from "lucide-react";
import { Button } from "@/components/ui/button";

/**
 * Transfer tickets are intentionally a separate backend capability. Keeping a
 * visible disabled state is safer than silently reintroducing STS or a browser
 * S3 client while the Zone Gateway presign route is not deployed.
 */
export function UploadModal({ isOpen, onClose }: { isOpen: boolean; onClose: () => void }) {
  if (!isOpen) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" role="dialog" aria-modal="true" aria-labelledby="storage-transfer-title">
      <div className="w-full max-w-md rounded-[8px] border border-border bg-card p-5 shadow-sm">
        <div className="flex items-start justify-between gap-3 border-b border-border pb-3">
          <div className="flex items-center gap-2"><AlertTriangle className="h-4 w-4 text-amber-500" /><h2 id="storage-transfer-title" className="text-sm font-semibold">Upload unavailable</h2></div>
          <button type="button" onClick={onClose} className="rounded-[4px] p-1 text-muted-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-blue-500" aria-label="Close upload dialog"><X className="h-4 w-4" /></button>
        </div>
        <p className="py-5 text-xs leading-relaxed text-muted-foreground">The Zone Storage Gateway transfer-ticket route is not enabled in this deployment. The Console will not request STS credentials or connect directly to S3.</p>
        <div className="flex justify-end"><Button variant="outline" onClick={onClose}>Close</Button></div>
      </div>
    </div>
  );
}
