"use client";

import { useState } from "react";
import { Check, Copy, Download, FileText, Loader2, Plus, Tag, Trash2, X } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import type { FileItem } from "./ObjectsTab";

export type ObjectDetails = {
  contentType?: string;
  etag?: string;
  customMetadata: Record<string, string>;
  tags: Record<string, string>;
  versionId?: string;
};

export function ObjectDetailPanel({
  selectedFile,
  fileDetails,
  metadataLoading,
  onClose,
  onSaveTags,
  onDelete,
  onDownload,
}: {
  selectedFile: FileItem;
  fileDetails: ObjectDetails | null;
  metadataLoading: boolean;
  onClose: () => void;
  onSaveTags: (tags: Record<string, string>) => Promise<void>;
  onDelete: () => Promise<void>;
  onDownload: () => void;
}) {
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [saving, setSaving] = useState(false);
  const [copied, setCopied] = useState(false);

  const save = async (next: Record<string, string>) => {
    setSaving(true);
    try {
      await onSaveTags(next);
      toast.success("Object tags updated.");
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Unable to update object tags.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <aside className="w-full min-w-0 border-border/60 lg:w-1/3 lg:border-l lg:pl-5" aria-label="Object details">
      <div className="flex items-start justify-between gap-3 border-b border-border/60 pb-3">
        <div className="flex min-w-0 items-center gap-2"><FileText className="h-8 w-8 shrink-0 text-purple-500" /><div className="min-w-0"><h2 className="truncate text-sm font-semibold">{selectedFile.name}</h2><p className="text-[10px] text-muted-foreground">Object detail</p></div></div>
        <button type="button" onClick={onClose} className="rounded-[4px] p-1 text-muted-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-blue-500" aria-label="Close details"><X className="h-4 w-4" /></button>
      </div>
      {metadataLoading ? <div className="flex items-center gap-2 py-6 text-xs text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" /> Loading metadata…</div> : (
        <div className="space-y-5 py-4 text-xs">
          <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-2 border-b border-border/60 pb-4"><span className="text-muted-foreground">Key</span><span className="break-all font-mono">{selectedFile.fullName}</span><span className="text-muted-foreground">Content type</span><span>{fileDetails?.contentType ?? "—"}</span><span className="text-muted-foreground">ETag</span><span className="break-all font-mono">{fileDetails?.etag ?? "—"}</span></div>
          <div className="flex gap-2"><Button size="sm" variant="outline" onClick={onDownload}><Download className="h-3.5 w-3.5" /> Download</Button><Button size="sm" variant="outline" onClick={() => { void navigator.clipboard.writeText(selectedFile.fullName); setCopied(true); window.setTimeout(() => setCopied(false), 1_500); }}>{copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />} Copy key</Button><Button size="sm" variant="outline" className="text-red-600 dark:text-red-400" onClick={() => void onDelete()}><Trash2 className="h-3.5 w-3.5" /> Delete</Button></div>
          <section className="border-b border-border/60 pb-4"><h3 className="mb-2 flex items-center gap-1.5 text-[11px] font-bold uppercase tracking-wider"><Tag className="h-3.5 w-3.5" /> Tags</h3><div className="space-y-2">{Object.entries(fileDetails?.tags ?? {}).map(([tagKey, tagValue]) => <div key={tagKey} className="flex items-center gap-2"><span className="min-w-0 flex-1 truncate font-mono">{tagKey}={tagValue}</span><button type="button" onClick={() => { const next = { ...(fileDetails?.tags ?? {}) }; delete next[tagKey]; void save(next); }} disabled={saving} className="text-red-500 focus-visible:ring-2 focus-visible:ring-blue-500" aria-label={`Remove tag ${tagKey}`}><Trash2 className="h-3.5 w-3.5" /></button></div>)}</div><div className="mt-3 flex gap-2"><input value={key} onChange={(event) => setKey(event.target.value)} placeholder="key" className="min-w-0 flex-1 rounded-[4px] border border-border bg-background px-2 py-1.5 text-xs outline-none focus-visible:ring-2 focus-visible:ring-blue-500" /><input value={value} onChange={(event) => setValue(event.target.value)} placeholder="value" className="min-w-0 flex-1 rounded-[4px] border border-border bg-background px-2 py-1.5 text-xs outline-none focus-visible:ring-2 focus-visible:ring-blue-500" /><Button size="icon-sm" variant="outline" disabled={!key.trim() || saving} onClick={() => { void save({ ...(fileDetails?.tags ?? {}), [key.trim()]: value }); setKey(""); setValue(""); }} aria-label="Add tag"><Plus className="h-3.5 w-3.5" /></Button></div></section>
          <p className="text-[11px] text-muted-foreground">Transfer links are issued only by the Zone Gateway and are never stored in browser storage.</p>
        </div>
      )}
    </aside>
  );
}
