"use client";

import { useCallback, useEffect, useState } from "react";
import {
  Check,
  Copy,
  Download,
  FileText,
  History,
  Info,
  Loader2,
  Plus,
  RotateCcw,
  Tag,
  Trash2,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import type { FileItem } from "./ObjectsTab";
import {
  deleteGatewayObjectVersion,
  downloadGatewayObjectVersion,
  listGatewayObjectVersions,
  restoreGatewayObjectVersion,
  type GatewayObjectVersion,
} from "@/features/storage/objects/api";

export type ObjectDetails = {
  contentType?: string;
  etag?: string;
  customMetadata: Record<string, string>;
  tags: Record<string, string>;
  versionId?: string;
};

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  return `${Number((bytes / 1024 ** index).toFixed(2))} ${units[index]}`;
}

function formatDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toISOString().replace("T", " ").slice(0, 19);
}

export function ObjectDetailPanel({
  selectedFile,
  fileDetails,
  metadataLoading,
  bucketName,
  accessSessionId,
  versioningEnabled,
  onClose,
  onSaveTags,
  onDelete,
  onDownload,
  onRefresh,
}: {
  selectedFile: FileItem;
  fileDetails: ObjectDetails | null;
  metadataLoading: boolean;
  bucketName: string;
  accessSessionId?: string;
  versioningEnabled?: boolean;
  onClose: () => void;
  onSaveTags: (tags: Record<string, string>) => Promise<void>;
  onDelete: () => Promise<void>;
  onDownload: () => void;
  onRefresh?: () => void;
}) {
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [saving, setSaving] = useState(false);
  const [copied, setCopied] = useState(false);
  const [copiedVersionId, setCopiedVersionId] = useState<string | null>(null);

  // Versions state
  const [versions, setVersions] = useState<GatewayObjectVersion[]>([]);
  const [versionsLoading, setVersionsLoading] = useState(false);
  const [versionActionLoading, setVersionActionLoading] = useState<string | null>(null);

  const loadVersions = useCallback(async () => {
    if (!versioningEnabled || !accessSessionId) return;
    try {
      setVersionsLoading(true);
      const list = await listGatewayObjectVersions(bucketName, selectedFile.fullName, accessSessionId);
      setVersions(list);
    } catch {
      // Fallback silently if versions cannot be listed
    } finally {
      setVersionsLoading(false);
    }
  }, [accessSessionId, bucketName, selectedFile.fullName, versioningEnabled]);

  useEffect(() => {
    let active = true;
    if (!versioningEnabled || !accessSessionId) {
      return undefined;
    }
    void listGatewayObjectVersions(bucketName, selectedFile.fullName, accessSessionId)
      .then((list) => {
        if (active) setVersions(list);
      })
      .catch(() => {
        if (active) setVersions([]);
      });
    return () => {
      active = false;
    };
  }, [accessSessionId, bucketName, selectedFile.fullName, versioningEnabled]);

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

  const handleDownloadVersion = async (ver: GatewayObjectVersion) => {
    if (!accessSessionId) return;
    try {
      setVersionActionLoading(`download-${ver.versionId}`);
      const blob = await downloadGatewayObjectVersion(
        bucketName,
        selectedFile.fullName,
        ver.versionId,
        accessSessionId,
      );
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `${selectedFile.name.replace(/\.[^/.]+$/, "")}-v${ver.versionId.slice(0, 8)}.${selectedFile.name.split(".").pop()}`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      toast.success(`Downloaded version ${ver.versionId.slice(0, 8)}`);
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Failed to download version");
    } finally {
      setVersionActionLoading(null);
    }
  };

  const handleRestoreVersion = async (ver: GatewayObjectVersion) => {
    if (!accessSessionId) return;
    try {
      setVersionActionLoading(`restore-${ver.versionId}`);
      await restoreGatewayObjectVersion(
        bucketName,
        selectedFile.fullName,
        ver.versionId,
        accessSessionId,
      );
      toast.success(`Restored version ${ver.versionId.slice(0, 8)} as the latest object revision`);
      await loadVersions();
      onRefresh?.();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Failed to restore version");
    } finally {
      setVersionActionLoading(null);
    }
  };

  const handleDeleteVersion = async (ver: GatewayObjectVersion) => {
    if (!accessSessionId) return;
    try {
      setVersionActionLoading(`delete-${ver.versionId}`);
      await deleteGatewayObjectVersion(
        bucketName,
        selectedFile.fullName,
        ver.versionId,
        accessSessionId,
      );
      toast.success(`Version ${ver.versionId.slice(0, 8)} permanently deleted`);
      await loadVersions();
      onRefresh?.();
    } catch (error) {
      toast.error(error instanceof Error ? error.message : "Failed to delete version");
    } finally {
      setVersionActionLoading(null);
    }
  };

  return (
    <aside className="w-full min-w-0 border-border/60 lg:w-1/3 lg:border-l lg:pl-5" aria-label="Object details">
      <div className="flex items-start justify-between gap-3 border-b border-border/60 pb-3">
        <div className="flex min-w-0 items-center gap-2">
          <FileText className="h-8 w-8 shrink-0 text-primary" />
          <div className="min-w-0">
            <h2 className="truncate text-sm font-semibold text-foreground">{selectedFile.name}</h2>
            <p className="text-[10px] text-muted-foreground">Object Details & History</p>
          </div>
        </div>
        <button
          type="button"
          onClick={onClose}
          className="rounded-[4px] p-1 text-muted-foreground hover:bg-muted focus-visible:ring-2 focus-visible:ring-primary"
          aria-label="Close details"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      {metadataLoading ? (
        <div className="flex items-center gap-2 py-6 text-xs text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" /> Loading metadata…
        </div>
      ) : (
        <div className="space-y-5 py-4 text-xs">
          {/* Metadata Grid */}
          <div className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-2 border-b border-border/60 pb-4">
            <span className="text-muted-foreground">Key</span>
            <span className="break-all font-mono text-foreground">{selectedFile.fullName}</span>
            <span className="text-muted-foreground">Content type</span>
            <span>{fileDetails?.contentType ?? "—"}</span>
            <span className="text-muted-foreground">ETag</span>
            <span className="break-all font-mono">{fileDetails?.etag ?? "—"}</span>
            {fileDetails?.versionId && (
              <>
                <span className="text-muted-foreground">Version ID</span>
                <span className="break-all font-mono text-[11px] text-indigo-400">{fileDetails.versionId}</span>
              </>
            )}
          </div>

          {/* Action Buttons */}
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="outline" onClick={onDownload}>
              <Download className="h-3.5 w-3.5 mr-1" /> Download
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                void navigator.clipboard.writeText(selectedFile.fullName);
                setCopied(true);
                window.setTimeout(() => setCopied(false), 1_500);
              }}
            >
              {copied ? <Check className="h-3.5 w-3.5 mr-1" /> : <Copy className="h-3.5 w-3.5 mr-1" />}
              Copy key
            </Button>
            <Button
              size="sm"
              variant="outline"
              className="text-destructive hover:bg-destructive/10"
              onClick={() => void onDelete()}
            >
              <Trash2 className="h-3.5 w-3.5 mr-1" /> Delete
            </Button>
          </div>

          {/* Version History Section */}
          <section className="border-b border-border/60 pb-4">
            <h3 className="mb-2 flex items-center justify-between text-[11px] font-bold uppercase tracking-wider text-muted-foreground">
              <span className="flex items-center gap-1.5 text-foreground">
                <History className="h-3.5 w-3.5 text-indigo-400" />
                Version History
              </span>
              {versioningEnabled && (
                <span className="text-[10px] text-emerald-500 font-normal">Versioning Active</span>
              )}
            </h3>

            {!versioningEnabled ? (
              <div className="rounded-lg border border-border/60 bg-muted/20 p-3 text-[11px] text-muted-foreground">
                <div className="flex items-start gap-2">
                  <Info className="h-4 w-4 shrink-0 text-muted-foreground mt-0.5" />
                  <p>Bucket Versioning is disabled. Only the latest object version is retained.</p>
                </div>
              </div>
            ) : versionsLoading ? (
              <div className="flex items-center gap-2 py-3 text-[11px] text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading version history…
              </div>
            ) : versions.length === 0 ? (
              <p className="text-[11px] text-muted-foreground py-1">No additional versions found.</p>
            ) : (
              <div className="space-y-2 max-h-56 overflow-y-auto pr-1">
                {versions.map((ver) => (
                  <div
                    key={ver.versionId}
                    className="flex flex-col gap-1.5 rounded-lg border border-border/60 bg-muted/20 p-2.5 text-[11px]"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex items-center gap-1.5 min-w-0">
                        {ver.isLatest && (
                          <span className="rounded bg-emerald-500/10 px-1.5 py-0.2 text-[10px] font-medium text-emerald-500">
                            Latest
                          </span>
                        )}
                        {ver.isDeleteMarker ? (
                          <span className="rounded bg-rose-500/10 px-1.5 py-0.2 text-[10px] font-medium text-rose-500">
                            Delete Marker
                          </span>
                        ) : (
                          <span className="text-muted-foreground">{formatBytes(ver.size)}</span>
                        )}
                      </div>
                      <span className="text-[10px] text-muted-foreground">{formatDate(ver.lastModified)}</span>
                    </div>

                    <div className="flex items-center justify-between gap-2">
                      <button
                        type="button"
                        onClick={() => {
                          void navigator.clipboard.writeText(ver.versionId);
                          setCopiedVersionId(ver.versionId);
                          window.setTimeout(() => setCopiedVersionId(null), 1500);
                        }}
                        className="truncate font-mono text-[10px] text-muted-foreground hover:text-foreground text-left"
                        title="Click to copy Version ID"
                      >
                        ID: {ver.versionId.slice(0, 16)}...
                        {copiedVersionId === ver.versionId && <span className="ml-1 text-emerald-500">✓</span>}
                      </button>

                      <div className="flex items-center gap-1 shrink-0">
                        {!ver.isDeleteMarker && (
                          <Button
                            size="icon"
                            variant="ghost"
                            className="h-6 w-6"
                            title="Download this version"
                            disabled={versionActionLoading === `download-${ver.versionId}`}
                            onClick={() => void handleDownloadVersion(ver)}
                          >
                            {versionActionLoading === `download-${ver.versionId}` ? (
                              <Loader2 className="h-3 w-3 animate-spin" />
                            ) : (
                              <Download className="h-3 w-3" />
                            )}
                          </Button>
                        )}
                        {!ver.isLatest && !ver.isDeleteMarker && (
                          <Button
                            size="icon"
                            variant="ghost"
                            className="h-6 w-6 text-indigo-400 hover:text-indigo-300"
                            title="Restore as latest version"
                            disabled={versionActionLoading === `restore-${ver.versionId}`}
                            onClick={() => void handleRestoreVersion(ver)}
                          >
                            {versionActionLoading === `restore-${ver.versionId}` ? (
                              <Loader2 className="h-3 w-3 animate-spin" />
                            ) : (
                              <RotateCcw className="h-3 w-3" />
                            )}
                          </Button>
                        )}
                        <Button
                          size="icon"
                          variant="ghost"
                          className="h-6 w-6 text-destructive hover:bg-destructive/10"
                          title="Permanently delete version"
                          disabled={versionActionLoading === `delete-${ver.versionId}`}
                          onClick={() => void handleDeleteVersion(ver)}
                        >
                          {versionActionLoading === `delete-${ver.versionId}` ? (
                            <Loader2 className="h-3 w-3 animate-spin" />
                          ) : (
                            <Trash2 className="h-3 w-3" />
                          )}
                        </Button>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </section>

          {/* Tags Section */}
          <section className="border-b border-border/60 pb-4">
            <h3 className="mb-2 flex items-center gap-1.5 text-[11px] font-bold uppercase tracking-wider text-muted-foreground">
              <Tag className="h-3.5 w-3.5 text-primary" /> Tags
            </h3>
            <div className="space-y-2">
              {Object.entries(fileDetails?.tags ?? {}).map(([tagKey, tagValue]) => (
                <div key={tagKey} className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate font-mono">
                    {tagKey}={tagValue}
                  </span>
                  <button
                    type="button"
                    onClick={() => {
                      const next = { ...(fileDetails?.tags ?? {}) };
                      delete next[tagKey];
                      void save(next);
                    }}
                    disabled={saving}
                    className="text-destructive hover:opacity-80 focus-visible:ring-2 focus-visible:ring-primary"
                    aria-label={`Remove tag ${tagKey}`}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
              ))}
            </div>
            <div className="mt-3 flex gap-2">
              <input
                value={key}
                onChange={(event) => setKey(event.target.value)}
                placeholder="key"
                className="min-w-0 flex-1 rounded-[4px] border border-border bg-background px-2 py-1.5 text-xs outline-none focus-visible:ring-2 focus-visible:ring-primary"
              />
              <input
                value={value}
                onChange={(event) => setValue(event.target.value)}
                placeholder="value"
                className="min-w-0 flex-1 rounded-[4px] border border-border bg-background px-2 py-1.5 text-xs outline-none focus-visible:ring-2 focus-visible:ring-primary"
              />
              <Button
                size="icon"
                variant="outline"
                className="h-8 w-8 shrink-0"
                disabled={!key.trim() || saving}
                onClick={() => {
                  void save({ ...(fileDetails?.tags ?? {}), [key.trim()]: value });
                  setKey("");
                  setValue("");
                }}
                aria-label="Add tag"
              >
                <Plus className="h-3.5 w-3.5" />
              </Button>
            </div>
          </section>

          <p className="text-[11px] text-muted-foreground">
            Transfer links and tickets are issued on-demand by the Zone Control Gateway and are never persisted in local storage.
          </p>
        </div>
      )}
    </aside>
  );
}
