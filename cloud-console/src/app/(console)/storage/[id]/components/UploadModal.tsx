import React, { useState, useRef } from "react";
import { Upload, X, File, CheckCircle, AlertCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";
import { type BucketItem } from "@/lib/api/storage";
import { invalidateObjectListCache } from "@/lib/cache/object-cache";
import { S3Client, PutObjectCommand } from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";

interface UploadModalProps {
  isOpen: boolean;
  onClose: () => void;
  bucket: BucketItem;
  currentPath: string[];
  fetchListObjects: () => void;
  getS3Client: () => Promise<S3Client>;
}

interface UploadingFile {
  id: string; // Unique key before event ID is generated
  file: File;
  name: string;
  size: number;
  progress: number; // 0 to 100
  status: "queued" | "uploading" | "success" | "error";
  error?: string;
  speed?: string;
  eta?: string;
}

export function UploadModal({
  isOpen,
  onClose,
  bucket,
  currentPath,
  fetchListObjects,
  getS3Client,
}: UploadModalProps) {
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [uploadQueue, setUploadQueue] = useState<UploadingFile[]>([]);

  if (!isOpen) return null;

  const formatBytes = (bytes: number): string => {
    if (bytes === 0) return "0 Bytes";
    const k = 1024;
    const sizes = ["Bytes", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + " " + sizes[i];
  };

  // [COMMENT]: Thực hiện tải lên một tệp tin duy nhất trực tiếp lên S3 bằng XHR
  const uploadSingleFile = async (item: UploadingFile) => {
    setUploadQueue((prev) =>
      prev.map((f) => (f.id === item.id ? { ...f, status: "uploading", progress: 0 } : f))
    );

    const key = currentPath.length > 0
      ? currentPath.join("/") + "/" + item.name
      : item.name;

    try {
      // 1. Lấy S3 Client và tự ký PUT URL cục bộ ở Client-side (0ms network cost)
      const client = await getS3Client();
      const command = new PutObjectCommand({
        Bucket: bucket.name,
        Key: key,
        ContentType: item.file.type,
      });
      const presignedUrl = await getSignedUrl(client, command, { expiresIn: 900 });

      // 2. Tiến hành upload bằng XHR để theo dõi tiến trình gửi dữ liệu lên
      const xhr = new XMLHttpRequest();
      xhr.open("PUT", presignedUrl, true);
      xhr.setRequestHeader("Content-Type", item.file.type);

      const startTime = Date.now();

      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable) {
          const percent = Math.round((event.loaded / event.total) * 100);
          const elapsed = (Date.now() - startTime) / 1000;
          let speedStr = "estimating...";
          let etaStr = "estimating...";

          if (elapsed > 0.1) {
            const speedBytes = event.loaded / elapsed;
            if (speedBytes > 1024 * 1024) {
              speedStr = `${(speedBytes / (1024 * 1024)).toFixed(1)} MB/s`;
            } else if (speedBytes > 1024) {
              speedStr = `${(speedBytes / 1024).toFixed(1)} KB/s`;
            } else {
              speedStr = `${speedBytes.toFixed(0)} B/s`;
            }

            const remaining = event.total - event.loaded;
            const etaSec = remaining / speedBytes;
            if (etaSec > 60) {
              etaStr = `${Math.floor(etaSec / 60)}m ${Math.floor(etaSec % 60)}s còn lại`;
            } else {
              etaStr = `${Math.round(etaSec)}s còn lại`;
            }
          }

          setUploadQueue((prev) =>
            prev.map((f) =>
              f.id === item.id
                ? { ...f, progress: percent, speed: speedStr, eta: etaStr }
                : f
            )
          );
        }
      };

      xhr.onload = () => {
        if (xhr.status >= 200 && xhr.status < 300) {
          setUploadQueue((prev) =>
            prev.map((f) =>
              f.id === item.id
                ? { ...f, status: "success", progress: 100, speed: undefined, eta: undefined }
                : f
            )
          );

          toast.success(`Tải lên tệp '${item.name}' thành công!`);

          invalidateObjectListCache(bucket.id);
          // Load lại danh sách sau khi hoàn thành
          setTimeout(() => {
            fetchListObjects();
          }, 400);
        } else {
          const errMsg = `S3 trả về status ${xhr.status}`;
          setUploadQueue((prev) =>
            prev.map((f) =>
              f.id === item.id
                ? { ...f, status: "error", error: errMsg, speed: undefined, eta: undefined }
                : f
            )
          );
          toast.error(`Tải lên tệp '${item.name}' thất bại: ${errMsg}`);
        }
      };

      xhr.onerror = () => {
        const errMsg = "Lỗi kết nối mạng";
        setUploadQueue((prev) =>
          prev.map((f) =>
            f.id === item.id
              ? { ...f, status: "error", error: errMsg, speed: undefined, eta: undefined }
              : f
          )
        );
        toast.error(`Tải lên tệp '${item.name}' thất bại do lỗi kết nối.`);
      };

      xhr.send(item.file);
    } catch (err: any) {
      const errMsg = err.message || "Tải lên thất bại";
      setUploadQueue((prev) =>
        prev.map((f) =>
          f.id === item.id
            ? { ...f, status: "error", error: errMsg, speed: undefined, eta: undefined }
            : f
        )
      );
      toast.error(`Không thể ký link tải lên cho '${item.name}': ${errMsg}`);
    }
  };

  // [COMMENT]: Xử lý chọn tệp từ file browser
  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files;
    if (!files || files.length === 0) return;

    const newFiles: UploadingFile[] = Array.from(files).map((f) => ({
      id: Math.random().toString(),
      file: f,
      name: f.name,
      size: f.size,
      progress: 0,
      status: "queued",
    }));

    setUploadQueue((prev) => [...prev, ...newFiles]);
    e.target.value = ""; // Clear để có thể chọn lại cùng file

    for (const item of newFiles) {
      uploadSingleFile(item);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/55 backdrop-blur-xs animate-in fade-in duration-200">
      <input
        type="file"
        ref={fileInputRef}
        className="hidden"
        multiple
        onChange={handleFileChange}
      />
      <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 shadow-2xl rounded-xl w-full max-w-lg overflow-hidden flex flex-col max-h-[85vh] animate-in zoom-in-95 duration-200 select-text">
        {/* Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-slate-100 dark:border-slate-800/80 shrink-0">
          <div className="flex items-center gap-2">
            <Upload className="h-4.5 w-4.5 text-blue-500" />
            <h3 className="text-sm font-bold text-slate-800 dark:text-slate-200">Tải lên đối tượng</h3>
          </div>
          <button
            onClick={() => {
              onClose();
              setUploadQueue([]);
            }}
            className="p-1 rounded-md hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer transition-colors"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Drop Zone & Queue List */}
        <div className="flex-1 overflow-y-auto p-5 space-y-4">
          {/* Drag and Drop Zone */}
          <div
            onClick={() => fileInputRef.current?.click()}
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              const files = e.dataTransfer.files;
              if (files && files.length > 0) {
                const newFiles: UploadingFile[] = Array.from(files).map((f) => ({
                  id: Math.random().toString(),
                  file: f,
                  name: f.name,
                  size: f.size,
                  progress: 0,
                  status: "queued",
                }));
                setUploadQueue((prev) => [...prev, ...newFiles]);
                for (const item of newFiles) {
                  uploadSingleFile(item);
                }
              }
            }}
            className="border-2 border-dashed border-slate-200 dark:border-slate-800 hover:border-blue-500 dark:hover:border-blue-400 rounded-xl p-8 text-center cursor-pointer transition-all hover:bg-slate-50/50 dark:hover:bg-slate-950/20 flex flex-col items-center justify-center gap-2 group"
          >
            <Upload className="h-8 w-8 text-slate-400 group-hover:text-blue-500 dark:group-hover:text-blue-400 transition-colors" />
            <p className="text-xs font-bold text-slate-700 dark:text-slate-300">
              Kéo thả file vào đây hoặc click để chọn file
            </p>
            <p className="text-[10px] text-slate-400">
              Hỗ trợ tải lên nhiều file cùng lúc
            </p>
          </div>

          {/* Uploading Queue List */}
          {uploadQueue.length > 0 && (
            <div className="space-y-3.5 pt-2">
              <h4 className="text-[11px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
                Danh sách tải lên ({uploadQueue.length})
              </h4>
              <div className="space-y-2.5 max-h-[30vh] overflow-y-auto pr-1">
                {uploadQueue.map((item) => (
                  <div
                    key={item.id}
                    className="p-3 border border-slate-100 dark:border-slate-800/80 bg-slate-50/50 dark:bg-slate-900/40 rounded-lg flex flex-col gap-1.5"
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div className="flex items-center gap-2 min-w-0">
                        <File className="h-4 w-4 text-slate-400 shrink-0" />
                        <div className="min-w-0">
                          <p className="text-xs font-bold text-slate-700 dark:text-slate-200 truncate leading-tight">
                            {item.name}
                          </p>
                          <p className="text-[10px] text-slate-400 mt-0.5">
                            {formatBytes(item.size)}
                          </p>
                        </div>
                      </div>

                      {/* Status Icon */}
                      <div className="shrink-0">
                        {item.status === "success" && (
                          <CheckCircle className="h-4.5 w-4.5 text-emerald-500" />
                        )}
                        {item.status === "error" && (
                          <span title={item.error}>
                            <AlertCircle className="h-4.5 w-4.5 text-rose-500" />
                          </span>
                        )}
                        {item.status === "uploading" && (
                          <span className="text-[10px] font-bold text-blue-500 dark:text-blue-400">
                            {item.progress}%
                          </span>
                        )}
                        {item.status === "queued" && (
                          <span className="text-[10px] font-bold text-slate-400">
                            0%
                          </span>
                        )}
                      </div>
                    </div>

                    {/* Progress Bar & ETA */}
                    {(item.status === "uploading" || item.status === "queued") && (
                      <div className="space-y-1">
                        <div className="w-full bg-slate-100 dark:bg-slate-800 h-1.5 rounded-full overflow-hidden">
                          <div
                            className="bg-blue-600 dark:bg-blue-500 h-full rounded-full transition-all duration-300"
                            style={{ width: `${item.progress}%` }}
                          />
                        </div>
                        <div className="flex justify-between items-center text-[9px] text-slate-400 font-mono">
                          <span>{item.speed || "calculating speed..."}</span>
                          <span>{item.eta || "calculating ETA..."}</span>
                        </div>
                      </div>
                    )}

                    {item.status === "error" && (
                      <p className="text-[10px] text-rose-500 font-medium">
                        Lỗi: {item.error || "Tải lên thất bại"}
                      </p>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="px-5 py-4 border-t border-slate-100 dark:border-slate-800/80 bg-slate-50/50 dark:bg-slate-900/30 flex items-center justify-end shrink-0">
          <Button
            onClick={() => {
              onClose();
              setUploadQueue([]);
            }}
            className="h-8.5 text-xs font-bold bg-slate-100 hover:bg-slate-200 dark:bg-slate-800 dark:hover:bg-slate-750 text-slate-700 dark:text-slate-300 cursor-pointer"
          >
            Đóng
          </Button>
        </div>
      </div>
    </div>
  );
}
