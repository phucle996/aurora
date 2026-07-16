import React from "react";
import { FileText, X, Loader2 } from "lucide-react";
import { GetObjectCommand, S3Client } from "@aws-sdk/client-s3";
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
  return (
    <div className="w-full lg:w-[33%] bg-transparent text-card-foreground pl-6 lg:border-l border-border/60 flex flex-col gap-4.5 animate-in slide-in-from-right duration-300 ease-in-out select-text text-xs">
      {/* Header */}
      <div className="flex items-center justify-between pb-3.5 border-b border-border/60 select-none">
        <div className="flex items-center gap-2">
          <FileText className="h-4.5 w-4.5 text-blue-500" />
          <h3 className="text-sm font-bold text-slate-800 dark:text-slate-200 truncate max-w-[280px]">
            Chi tiết đối tượng
          </h3>
        </div>
        <button
          onClick={onClose}
          className="p-1 rounded-md hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer outline-none"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      {/* Content */}
      <div className="space-y-6">
        {/* Overview */}
        <div className="bg-slate-50 dark:bg-slate-900/40 p-4 rounded-xl border border-slate-100 dark:border-slate-800/80 space-y-3">
          <p className="text-xs font-bold text-slate-700 dark:text-slate-300 truncate" title={selectedFile.fullName}>
            Key: <span className="font-mono text-slate-500 font-medium">{selectedFile.fullName}</span>
          </p>
          <div className="grid grid-cols-2 gap-4 text-[11px] text-slate-500 font-medium">
            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400">Dung lượng</span>
              <span className="text-slate-700 dark:text-slate-200 font-bold">{selectedFile.size}</span>
            </div>
            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400">Cập nhật lúc</span>
              <span className="text-slate-700 dark:text-slate-200 font-bold">{selectedFile.lastModified}</span>
            </div>
          </div>
        </div>

        {metadataLoading ? (
          <div className="flex flex-col items-center justify-center py-12 text-slate-400">
            <Loader2 className="h-6 w-6 animate-spin text-blue-500 mb-2" />
            <span className="text-[10px] font-bold uppercase tracking-wider">Đang tải metadata...</span>
          </div>
        ) : fileDetails ? (
          <>
            {/* System Properties */}
            <div className="space-y-2">
              <h4 className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
                Thuộc tính hệ thống
              </h4>
              <div className="border border-slate-100 dark:border-slate-800/80 rounded-lg overflow-hidden divide-y divide-slate-100 dark:divide-slate-800/80 text-[11px]">
                <div className="px-3.5 py-2 flex justify-between">
                  <span className="text-slate-400">Content-Type</span>
                  <span className="font-mono font-bold text-slate-700 dark:text-slate-200">{fileDetails.contentType || "binary/octet-stream"}</span>
                </div>
                <div className="px-3.5 py-2 flex justify-between">
                  <span className="text-slate-400">ETag</span>
                  <span className="font-mono font-bold text-slate-700 dark:text-slate-200 truncate max-w-[200px]" title={fileDetails.etag}>
                    {fileDetails.etag || "—"}
                  </span>
                </div>
                {fileDetails.versionId && (
                  <div className="px-3.5 py-2 flex justify-between">
                    <span className="text-slate-400">Version ID</span>
                    <span className="font-mono font-bold text-slate-700 dark:text-slate-200 truncate max-w-[200px]">{fileDetails.versionId}</span>
                  </div>
                )}
              </div>
            </div>

            {/* Custom Metadata */}
            <div className="space-y-2">
              <h4 className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
                Custom Metadata (User-defined)
              </h4>
              {!fileDetails.customMetadata || Object.keys(fileDetails.customMetadata).length === 0 ? (
                <p className="text-[11px] text-slate-400 italic">Không có custom metadata</p>
              ) : (
                <div className="border border-slate-100 dark:border-slate-800/80 rounded-lg overflow-hidden divide-y divide-slate-100 dark:divide-slate-800/80 text-[11px]">
                  {Object.entries(fileDetails.customMetadata).map(([key, value]) => (
                    <div key={key} className="px-3.5 py-2 flex justify-between">
                      <span className="text-slate-400 font-mono">{key}</span>
                      <span className="font-semibold text-slate-700 dark:text-slate-200">{value}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>

            {/* Object Tags */}
            <div className="space-y-2">
              <h4 className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
                Nhãn dữ liệu (Tags)
              </h4>
              {!fileDetails.tags || Object.keys(fileDetails.tags).length === 0 ? (
                <p className="text-[11px] text-slate-400 italic">Không có nhãn (tags) được gán</p>
              ) : (
                <div className="flex flex-wrap gap-1.5">
                  {Object.entries(fileDetails.tags).map(([key, value]) => (
                    <span key={key} className="inline-flex items-center px-2 py-1 rounded bg-blue-50 dark:bg-blue-950/40 text-blue-600 dark:text-blue-400 font-medium text-[10px] border border-blue-100/60 dark:border-blue-900/40">
                      {key}={value}
                    </span>
                  ))}
                </div>
              )}
            </div>
          </>
        ) : (
          <p className="text-[11px] text-rose-500 text-center font-bold">Không thể tải thông tin tệp tin</p>
        )}
      </div>

      {/* Footer Actions */}
      <div className="pt-4 border-t border-border/60 flex items-center justify-between">
        <Button
          variant="outline"
          onClick={async () => {
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
          }}
          className="h-8.5 text-[11px] font-bold bg-white hover:bg-slate-50 border-slate-200 dark:border-slate-800 dark:bg-slate-900 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-300 cursor-pointer flex-1 mr-2"
        >
          Tải xuống
        </Button>
        <Button
          onClick={async () => {
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
          }}
          className="h-8.5 text-[11px] font-bold bg-rose-600 hover:bg-rose-700 text-white cursor-pointer flex-1 ml-2"
        >
          Xóa bỏ
        </Button>
      </div>
    </div>
  );
}
