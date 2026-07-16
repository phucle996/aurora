import React, { useState, useEffect } from "react";
import {
  FileText,
  X,
  Loader2,
  Download,
  Share2,
  Eye,
  Scale,
  Lock,
  Tag,
  Info,
  Layers,
  Trash2,
  Plus,
  Copy,
  Check,
  Globe,
  Database,
  FileCode,
} from "lucide-react";
import { GetObjectCommand, PutObjectTaggingCommand, S3Client } from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { type FileItem } from "./ObjectsTab";

interface ObjectDetailPanelProps {
  selectedFile: FileItem;
  onClose: () => void;
  metadataLoading: boolean;
  fileDetails: {
    contentType?: string;
    etag?: string;
    customMetadata?: Record<string, string>;
    tags?: Record<string, string>;
    versionId?: string;
  } | null;
  bucket: {
    id: string;
    name: string;
  };
  getS3Client: () => Promise<S3Client>;
  deleteSingleItem: (key: string) => Promise<void>;
  invalidateObjectListCache: (bucketId: string) => void;
  fetchListObjects: () => void;
}

export function ObjectDetailPanel({
  selectedFile,
  onClose,
  metadataLoading,
  fileDetails,
  bucket,
  getS3Client,
  deleteSingleItem,
  invalidateObjectListCache,
  fetchListObjects,
}: ObjectDetailPanelProps) {
  const [localTags, setLocalTags] = useState<Record<string, string>>({});
  const [newTagKey, setNewTagKey] = useState("");
  const [newTagVal, setNewTagVal] = useState("");
  const [isAddingTag, setIsAddingTag] = useState(false);
  const [tagPending, setTagPending] = useState(false);
  const [shareCopied, setShareCopied] = useState(false);

  useEffect(() => {
    if (fileDetails?.tags) {
      setLocalTags(fileDetails.tags);
    } else {
      setLocalTags({});
    }
  }, [fileDetails]);

  // Tải xuống file thực tế
  const handleDownload = async () => {
    const toastId = toast.loading("Đang chuẩn bị tải xuống...");
    try {
      const client = await getS3Client();
      const command = new GetObjectCommand({
        Bucket: bucket.name,
        Key: selectedFile.fullName,
      });
      const url = await getSignedUrl(client, command, { expiresIn: 900 });
      const link = document.createElement("a");
      link.href = url;
      link.setAttribute("download", selectedFile.name);
      document.body.appendChild(link);
      link.click();
      document.body.removeChild(link);
      toast.success("Đang tải file xuống!", { id: toastId });
    } catch (err: any) {
      toast.error(err.message || "Tải xuống thất bại", { id: toastId });
    }
  };

  // Chia sẻ URL hết hạn sau 24h
  const handleShare = async () => {
    const toastId = toast.loading("Đang tạo liên kết chia sẻ...");
    try {
      const client = await getS3Client();
      const command = new GetObjectCommand({
        Bucket: bucket.name,
        Key: selectedFile.fullName,
      });
      const url = await getSignedUrl(client, command, { expiresIn: 86400 });
      await navigator.clipboard.writeText(url);
      setShareCopied(true);
      setTimeout(() => setShareCopied(false), 2000);
      toast.success("Đã copy liên kết tải xuống (hiệu lực 24h) vào clipboard!", { id: toastId });
    } catch (err: any) {
      toast.error("Không thể tạo liên kết chia sẻ: " + err.message, { id: toastId });
    }
  };

  // Lưu tag lên S3
  const handleSaveTags = async (updatedTags: Record<string, string>) => {
    setTagPending(true);
    const toastId = toast.loading("Đang cập nhật nhãn tags...");
    try {
      const client = await getS3Client();
      const tagSet = Object.entries(updatedTags).map(([Key, Value]) => ({
        Key,
        Value,
      }));
      const command = new PutObjectTaggingCommand({
        Bucket: bucket.name,
        Key: selectedFile.fullName,
        Tagging: { TagSet: tagSet },
      });
      await client.send(command);
      setLocalTags(updatedTags);
      toast.success("Cập nhật nhãn tags thành công!", { id: toastId });
      invalidateObjectListCache(bucket.id);
    } catch (err: any) {
      toast.error(err.message || "Không thể cập nhật nhãn tags.", { id: toastId });
    } finally {
      setTagPending(false);
    }
  };

  const handleAddTag = () => {
    if (!newTagKey.trim() || !newTagVal.trim()) {
      toast.error("Key và Value không được để trống");
      return;
    }
    const updated = { ...localTags, [newTagKey.trim()]: newTagVal.trim() };
    handleSaveTags(updated);
    setNewTagKey("");
    setNewTagVal("");
    setIsAddingTag(false);
  };

  const handleRemoveTag = (key: string) => {
    const updated = { ...localTags };
    delete updated[key];
    handleSaveTags(updated);
  };

  const handleDeleteItem = async () => {
    if (!window.confirm("Bạn có chắc chắn muốn xóa đối tượng này? Hành động không thể hoàn tác.")) {
      return;
    }
    const toastId = toast.loading("Đang xóa...");
    try {
      await deleteSingleItem(selectedFile.fullName);
      toast.success("Đã xóa đối tượng thành công!", { id: toastId });
      onClose();
      invalidateObjectListCache(bucket.id);
      fetchListObjects();
    } catch (err: any) {
      toast.error(err.message || "Xóa thất bại", { id: toastId });
    }
  };

  const getFileIcon = (name: string) => {
    return <FileText className="h-9 w-9 text-purple-600 dark:text-purple-400 shrink-0" />;
  };

  return (
    <div className="w-full lg:w-[33%] bg-transparent text-card-foreground pl-6 lg:border-l border-border/60 flex flex-col gap-4.5 animate-in slide-in-from-right duration-300 ease-in-out select-text text-xs">
      
      {/* Title & Icon Header */}
      <div className="flex items-center justify-between pb-3.5 border-b border-border/60 select-none">
        <div className="flex items-center gap-3">
          {getFileIcon(selectedFile.name)}
          <div className="flex flex-col min-w-0">
            <h3 className="text-sm font-bold text-slate-800 dark:text-slate-200 truncate max-w-[200px]" title={selectedFile.name}>
              {selectedFile.name}
            </h3>
            <span className="text-[10px] text-slate-400 font-medium">Đối tượng lưu trữ</span>
          </div>
        </div>
        <button
          onClick={onClose}
          className="p-1 rounded-md hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer outline-none"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      {/* Main Container */}
      <div className="flex-1 overflow-y-auto pr-1 space-y-6">
        
        {/* Actions Menu */}
        <div className="space-y-2">
          <h4 className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
            Actions:
          </h4>
          <div className="border border-slate-200 dark:border-slate-800 rounded-lg overflow-hidden divide-y divide-slate-100 dark:divide-slate-800/80 bg-white dark:bg-slate-900">
            {/* Download */}
            <button
              onClick={handleDownload}
              className="w-full px-4 py-2.5 text-left flex items-center gap-2.5 hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer transition-colors text-slate-700 dark:text-slate-200 font-semibold"
            >
              <Download size={14} className="text-slate-400" />
              <span>Download</span>
            </button>

            {/* Share */}
            <button
              onClick={handleShare}
              className="w-full px-4 py-2.5 text-left flex items-center justify-between hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer transition-colors text-slate-700 dark:text-slate-200 font-semibold"
            >
              <div className="flex items-center gap-2.5">
                <Share2 size={14} className="text-slate-400" />
                <span>Share</span>
              </div>
              {shareCopied ? (
                <Check size={13} className="text-emerald-500" />
              ) : (
                <Copy size={12} className="text-slate-400" />
              )}
            </button>

            {/* Preview (Mocked) */}
            <button
              disabled
              className="w-full px-4 py-2.5 text-left flex items-center gap-2.5 opacity-50 cursor-not-allowed text-slate-400 dark:text-slate-500 font-semibold"
            >
              <Eye size={14} />
              <span>Preview</span>
            </button>

            {/* Legal Hold (Mocked) */}
            <button
              disabled
              className="w-full px-4 py-2.5 text-left flex items-center gap-2.5 opacity-50 cursor-not-allowed text-slate-400 dark:text-slate-500 font-semibold"
            >
              <Scale size={14} />
              <span>Legal Hold</span>
            </button>

            {/* Retention (Mocked) */}
            <button
              disabled
              className="w-full px-4 py-2.5 text-left flex items-center gap-2.5 opacity-50 cursor-not-allowed text-slate-400 dark:text-slate-500 font-semibold"
            >
              <Lock size={14} />
              <span>Retention</span>
            </button>

            {/* Add/View Tags */}
            <button
              onClick={() => setIsAddingTag(true)}
              className="w-full px-4 py-2.5 text-left flex items-center gap-2.5 hover:bg-slate-50 dark:hover:bg-slate-800/50 cursor-pointer transition-colors text-slate-700 dark:text-slate-200 font-semibold"
            >
              <Tag size={14} className="text-slate-400" />
              <span>Tags</span>
            </button>

            {/* Inspect (Mocked) */}
            <button
              disabled
              className="w-full px-4 py-2.5 text-left flex items-center gap-2.5 opacity-50 cursor-not-allowed text-slate-400 dark:text-slate-500 font-semibold"
            >
              <Info size={14} />
              <span>Inspect</span>
            </button>

            {/* Display Versions (Mocked) */}
            <button
              disabled
              className="w-full px-4 py-2.5 text-left flex items-center gap-2.5 opacity-50 cursor-not-allowed text-slate-400 dark:text-slate-500 font-semibold"
            >
              <Layers size={14} />
              <span>Display Object Versions</span>
            </button>
          </div>

          {/* Delete Button */}
          <button
            onClick={handleDeleteItem}
            className="w-full h-9 mt-1 rounded-lg border border-red-500 text-red-500 hover:bg-red-50 dark:hover:bg-red-950/20 bg-transparent flex items-center justify-center gap-2 font-bold cursor-pointer transition-colors"
          >
            <Trash2 size={14} />
            <span>Delete</span>
          </button>
        </div>

        {/* Object Info */}
        <div className="space-y-3">
          <div className="flex justify-between items-center border-b border-border pb-1.5">
            <h4 className="text-[12px] font-bold text-slate-800 dark:text-slate-200">
              Object Info
            </h4>
            <Database size={15} className="text-slate-400" />
          </div>

          <div className="space-y-3 font-semibold text-slate-700 dark:text-slate-300">
            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400 font-bold">Name:</span>
              <span className="text-[11px] truncate block select-all" title={selectedFile.fullName}>{selectedFile.fullName}</span>
            </div>
            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400 font-bold">Size:</span>
              <span className="text-[11px] text-slate-900 dark:text-slate-100 font-bold">{selectedFile.size}</span>
            </div>
            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400 font-bold">Last Modified:</span>
              <span className="text-[11px]">{selectedFile.lastModified}</span>
            </div>
            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400 font-bold">ETAG:</span>
              <span className="text-[11px] font-mono break-all text-slate-500 font-medium" title={fileDetails?.etag}>
                {fileDetails?.etag || "—"}
              </span>
            </div>

            {/* Tags section */}
            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400 font-bold mb-1">Tags:</span>
              {tagPending ? (
                <div className="flex items-center gap-1.5 text-slate-400 text-[10px]">
                  <Loader2 size={12} className="animate-spin" />
                  <span>Đang cập nhật...</span>
                </div>
              ) : Object.keys(localTags).length === 0 ? (
                <span className="text-[11px] text-slate-400 italic">N/A</span>
              ) : (
                <div className="flex flex-wrap gap-1.5">
                  {Object.entries(localTags).map(([key, val]) => (
                    <span
                      key={key}
                      onClick={() => handleRemoveTag(key)}
                      className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400 border border-blue-100/60 dark:border-blue-900/40 text-[10px] font-bold cursor-pointer group hover:bg-rose-50 hover:text-rose-600 hover:border-rose-200 transition-colors"
                      title="Click để xóa nhãn này"
                    >
                      <span>{key}={val}</span>
                      <X size={10} className="opacity-60 group-hover:opacity-100 shrink-0" />
                    </span>
                  ))}
                </div>
              )}

              {/* Tag creation form */}
              {isAddingTag && (
                <div className="mt-2.5 p-3.5 bg-slate-50 dark:bg-slate-900/50 border border-slate-200 dark:border-slate-800 rounded-lg space-y-2.5 animate-in fade-in zoom-in-95 duration-150">
                  <span className="block text-[9px] uppercase tracking-wider text-slate-400 font-extrabold">Thêm nhãn (New Tag)</span>
                  <div className="grid grid-cols-2 gap-2">
                    <input
                      type="text"
                      placeholder="Key"
                      value={newTagKey}
                      onChange={(e) => setNewTagKey(e.target.value)}
                      className="px-2 py-1 rounded border border-slate-200 dark:border-slate-800 bg-transparent text-[11px] focus:outline-none"
                    />
                    <input
                      type="text"
                      placeholder="Value"
                      value={newTagVal}
                      onChange={(e) => setNewTagVal(e.target.value)}
                      className="px-2 py-1 rounded border border-slate-200 dark:border-slate-800 bg-transparent text-[11px] focus:outline-none"
                    />
                  </div>
                  <div className="flex items-center justify-end gap-1.5">
                    <button
                      onClick={() => setIsAddingTag(false)}
                      className="h-6 px-2.5 rounded bg-white hover:bg-slate-100 border border-slate-200 text-slate-600 dark:bg-slate-900 dark:border-slate-800 dark:text-slate-300 cursor-pointer font-bold"
                    >
                      Hủy
                    </button>
                    <button
                      onClick={handleAddTag}
                      disabled={tagPending}
                      className="h-6 px-2.5 rounded bg-blue-600 hover:bg-blue-700 text-white font-bold cursor-pointer disabled:opacity-50"
                    >
                      Lưu
                    </button>
                  </div>
                </div>
              )}
            </div>

            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400 font-bold">Legal Hold:</span>
              <span className="text-[11px]">Off</span>
            </div>
            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400 font-bold">Retention Policy:</span>
              <span className="text-[11px]">None</span>
            </div>
          </div>
        </div>

        {/* Metadata */}
        <div className="space-y-3">
          <div className="flex justify-between items-center border-b border-border pb-1.5">
            <h4 className="text-[12px] font-bold text-slate-800 dark:text-slate-200">
              Metadata
            </h4>
            <FileCode size={15} className="text-slate-400" />
          </div>

          <div className="space-y-3 text-[11px] font-semibold text-slate-700 dark:text-slate-300">
            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400 font-bold">Content-Type</span>
              <span className="font-mono text-slate-500 font-medium">{fileDetails?.contentType || "application/octet-stream"}</span>
            </div>
          </div>
        </div>

      </div>
    </div>
  );
}
