"use client";

import { useCallback, useMemo, useState } from "react";
import { ChevronRight, Download, File, FileText, Folder, HardDrive, Image as ImageIcon, Loader2, RefreshCw, Trash2, Upload } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { useUserSession } from "@/session/use-session";
import { useConsoleQueryScope } from "@/shared/query/scope";
import { type BucketItem } from "@/features/storage/api";
import {
  bulkDeleteGatewayObjects,
  downloadGatewayObject,
  getGatewayTags,
  headGatewayObject,
  listGatewayObjects,
  putGatewayTags,
  uploadGatewayObject,
  type GatewayObject,
  type StorageGatewayError,
} from "@/features/storage/objects/api";
import { useStorageAccessSession } from "@/features/storage/objects/access-session";
import { UploadModal } from "./UploadModal";
import { DeleteConfirmModal } from "./DeleteConfirmModal";
import { ObjectDetailPanel, type ObjectDetails } from "./ObjectDetailPanel";

export type FileItem = {
  name: string;
  fullName: string;
  type: "folder" | "file";
  size?: string;
  lastModified?: string;
};

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 Bytes";
  const units = ["Bytes", "KB", "MB", "GB", "TB"];
  const index = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  return `${Number((bytes / 1024 ** index).toFixed(2))} ${units[index]}`;
}

function formatDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toISOString().replace("T", " ").slice(0, 19);
}

function gatewayMessage(error: unknown): string {
  const gateway = error as Partial<StorageGatewayError>;
  if (gateway?.status === 403) return "Storage access is preparing, expired, revoked, or forbidden. Try again after the Zone projection catches up.";
  if (gateway?.status === 404 || gateway?.status === 501) return "Storage gateway route is not available in this deployment.";
  return error instanceof Error ? error.message : "Storage Gateway request failed.";
}

export function ObjectsTab({ bucket }: { bucket: BucketItem }) {
  const { generation } = useUserSession();
  const scope = useConsoleQueryScope();
  const queryClient = useQueryClient();
  const access = useStorageAccessSession(bucket.id);
  const [currentPath, setCurrentPath] = useState<string[]>([]);
  const [selectedItems, setSelectedItems] = useState<string[]>([]);
  const [selectedFile, setSelectedFile] = useState<FileItem | null>(null);
  const [fileDetails, setFileDetails] = useState<ObjectDetails | null>(null);
  const [metadataLoading, setMetadataLoading] = useState(false);
  const [uploadOpen, setUploadOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const objectsQuery = useQuery<GatewayObject[]>({
    queryKey: [...scope, "storage", "objects", bucket.id],
    queryFn: ({ signal }) => access.execute((session, operationSignal) => listGatewayObjects(bucket.name, session, operationSignal), signal),
    enabled: Boolean(bucket.id && generation),
    retry: false,
  });

  const items = useMemo(() => {
    const allObjects = objectsQuery.data ?? [];
    const prefix = currentPath.length ? `${currentPath.join("/")}/` : "";
    const result = new Map<string, FileItem>();
    for (const object of allObjects) {
      if (!object.key.startsWith(prefix)) continue;
      const relative = object.key.slice(prefix.length);
      if (!relative) continue;
      const slash = relative.indexOf("/");
      if (slash === -1) {
        result.set(relative, { name: relative, fullName: object.key, type: "file", size: formatBytes(object.size), lastModified: formatDate(object.last_modified) });
      } else {
        const folder = relative.slice(0, slash);
        result.set(folder, { name: folder, fullName: `${prefix}${folder}`, type: "folder" });
      }
    }
    return [...result.values()];
  }, [currentPath, objectsQuery.data]);

  const refetchObjects = objectsQuery.refetch;
  const refresh = useCallback(() => {
    setSelectedItems([]);
    return refetchObjects();
  }, [refetchObjects]);

  const openDetails = useCallback(async (file: FileItem) => {
    setSelectedFile(file);
    setMetadataLoading(true);
    try {
      const details = await access.execute(async (session, signal) => {
        const [head, tags] = await Promise.all([
          headGatewayObject(bucket.name, file.fullName, session, signal),
          getGatewayTags(bucket.name, file.fullName, session, signal),
        ]);
        return { ...head, tags };
      });
      setFileDetails(details);
    } catch (error) {
      setFileDetails(null);
      toast.error(gatewayMessage(error));
    } finally {
      setMetadataLoading(false);
    }
  }, [access, bucket.name]);

  const deleteSelected = useCallback(async () => {
    if (!selectedItems.length) return;
    try {
      await access.execute((session, signal) => bulkDeleteGatewayObjects(bucket.name, selectedItems, session, signal));
      toast.success(`Deleted ${selectedItems.length} object(s).`);
      setSelectedItems([]);
      setDeleteOpen(false);
      await queryClient.invalidateQueries({ queryKey: [...scope, "storage", "objects", bucket.id] });
    } catch (error) {
      toast.error(gatewayMessage(error));
    }
  }, [access, bucket.id, bucket.name, queryClient, scope, selectedItems]);

  const saveTags = useCallback(async (tags: Record<string, string>) => {
    if (!selectedFile) return;
    await access.execute((session, signal) => putGatewayTags(bucket.name, selectedFile.fullName, tags, session, signal));
    setFileDetails((current) => current ? { ...current, tags } : current);
  }, [access, bucket.name, selectedFile]);

  const uploadObject = useCallback(async (file: File, objectKey: string) => {
    await access.execute((session, signal) => uploadGatewayObject(bucket.name, objectKey, file, session, signal));
  }, [access, bucket.name]);

  const downloadObject = useCallback(async (objectKey: string) => {
    try {
      const blob = await access.execute((session, signal) => downloadGatewayObject(bucket.name, objectKey, session, signal));
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = objectKey.split("/").at(-1) || "download";
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 30_000);
    } catch (error) {
      toast.error(gatewayMessage(error));
    }
  }, [access, bucket.name]);

  const selectedFiles = items.filter((item) => item.type === "file");

  return (
    <div className="flex w-full flex-col gap-4 py-4 lg:flex-row lg:items-stretch">
      <div className={cn("min-w-0 space-y-4", selectedFile ? "lg:w-2/3" : "w-full")}>
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3">
          <div className="flex min-w-0 items-center gap-1 overflow-x-auto text-[13px] font-semibold">
            <button type="button" onClick={() => { setCurrentPath([]); setSelectedItems([]); }} className="flex shrink-0 items-center gap-1 text-muted-foreground hover:text-foreground focus-visible:ring-2 focus-visible:ring-blue-500">
              <HardDrive className="h-4 w-4" /> {bucket.name}
            </button>
            {currentPath.map((part, index) => (
              <span className="contents" key={`${part}-${index}`}>
                <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                <button type="button" onClick={() => { setCurrentPath(currentPath.slice(0, index + 1)); setSelectedItems([]); }} className="max-w-[140px] truncate hover:text-foreground focus-visible:ring-2 focus-visible:ring-blue-500">
                  {part}
                </button>
              </span>
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => void refresh()} disabled={objectsQuery.isFetching} aria-label="Refresh objects">
              <RefreshCw className={cn("h-3.5 w-3.5", objectsQuery.isFetching && "animate-spin")} />
            </Button>
            <Button variant="outline" size="sm" onClick={() => { if (selectedItems.length === 1) void downloadObject(selectedItems[0]); else toast.info("Select one object to download."); }} disabled={!selectedItems.length}>
              <Download className="h-3.5 w-3.5" /> Download
            </Button>
            <Button variant="outline" size="sm" onClick={() => setDeleteOpen(true)} disabled={!selectedItems.length} className="text-red-600 dark:text-red-400">
              <Trash2 className="h-3.5 w-3.5" /> Delete
            </Button>
            <Button size="sm" onClick={() => setUploadOpen(true)}>
              <Upload className="h-3.5 w-3.5" /> Upload
            </Button>
          </div>
        </div>

        {objectsQuery.isLoading ? (
          <div className="flex flex-col items-center justify-center py-20 text-muted-foreground"><Loader2 className="mb-2 h-7 w-7 animate-spin text-blue-500" /><span className="text-xs">Preparing Zone Control Edge Gateway…</span></div>
        ) : objectsQuery.error ? (
          <div className="border border-dashed border-red-500/30 bg-red-500/5 p-10 text-center text-sm text-red-500"><p className="font-semibold">Storage Gateway unavailable</p><p className="mt-1 text-xs">{gatewayMessage(objectsQuery.error)}</p><Button className="mt-4" variant="outline" onClick={() => void refresh()}>Retry</Button></div>
        ) : (
          <div className="overflow-x-auto rounded-[6px] border border-border">
            <table className="w-full min-w-[620px] border-collapse text-left text-[13px]">
              <thead><tr className="border-b border-border bg-muted/30 text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
                <th className="w-12 px-4 py-3 text-center"><input type="checkbox" aria-label="Select visible files" checked={selectedFiles.length > 0 && selectedFiles.every((item) => selectedItems.includes(item.fullName))} onChange={(event) => setSelectedItems(event.target.checked ? selectedFiles.map((item) => item.fullName) : [])} /></th>
                <th className="px-3 py-3">Name</th><th className="px-3 py-3">Size</th><th className="px-3 py-3">Last modified</th>
              </tr></thead>
              <tbody className="divide-y divide-border/60">
                {items.map((item) => <tr key={item.fullName} className={cn("hover:bg-muted/30", selectedItems.includes(item.fullName) && "bg-muted/50")}>
                  <td className="px-4 py-3 text-center">{item.type === "file" && <input type="checkbox" aria-label={`Select ${item.name}`} checked={selectedItems.includes(item.fullName)} onChange={() => setSelectedItems((current) => current.includes(item.fullName) ? current.filter((value) => value !== item.fullName) : [...current, item.fullName])} />}</td>
                  <td className="max-w-[420px] px-3 py-3"><button type="button" onClick={() => item.type === "folder" ? (setCurrentPath([...currentPath, item.name]), setSelectedItems([])) : void openDetails(item)} className="flex max-w-full items-center gap-2 font-semibold hover:text-blue-500 focus-visible:ring-2 focus-visible:ring-blue-500">{item.type === "folder" ? <Folder className="h-4 w-4 shrink-0 text-blue-500" /> : item.name.match(/\.(png|jpe?g|gif|webp)$/i) ? <ImageIcon className="h-4 w-4 shrink-0 text-emerald-500" /> : item.name.match(/\.(json|md|sql|txt)$/i) ? <FileText className="h-4 w-4 shrink-0 text-amber-500" /> : <File className="h-4 w-4 shrink-0 text-muted-foreground" />}<span className="truncate">{item.name}</span></button></td>
                  <td className="whitespace-nowrap px-3 py-3 text-muted-foreground">{item.size ?? "—"}</td><td className="whitespace-nowrap px-3 py-3 text-muted-foreground">{item.lastModified ?? "—"}</td>
                </tr>)}
                {!items.length && <tr><td colSpan={4} className="py-16 text-center text-sm text-muted-foreground">This directory contains no objects.</td></tr>}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {selectedFile && <ObjectDetailPanel selectedFile={selectedFile} fileDetails={fileDetails} metadataLoading={metadataLoading} onClose={() => { setSelectedFile(null); setFileDetails(null); }} onSaveTags={saveTags} onDelete={async () => { await deleteSelected(); setSelectedFile(null); }} onDownload={() => void downloadObject(selectedFile.fullName)} />}
      <UploadModal
        isOpen={uploadOpen}
        prefix={currentPath.length ? `${currentPath.join("/")}/` : ""}
        onClose={() => setUploadOpen(false)}
        onUpload={uploadObject}
        onUploaded={() => { void queryClient.invalidateQueries({ queryKey: [...scope, "storage", "objects", bucket.id] }); }}
      />
      <DeleteConfirmModal isOpen={deleteOpen} onClose={() => setDeleteOpen(false)} onConfirm={() => void deleteSelected()} items={selectedItems} isDeleting={false} />
    </div>
  );
}
