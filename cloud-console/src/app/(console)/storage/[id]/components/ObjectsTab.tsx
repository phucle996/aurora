import React, { useState, useEffect, useRef } from "react";
import {
  Folder,
  File,
  Upload,
  Download,
  Trash2,
  ChevronRight,
  HardDrive,
  FileText,
  Image,
  RefreshCw,
  Loader2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { type BucketItem, registerObjectPresignRequest } from "@/lib/api/storage";
import { useRealtime } from "@/context/RealtimeContext";
import {
  getCachedObjectList,
  setCachedObjectList,
  invalidateObjectListCache,
  getCachedPresignUrl,
  setCachedPresignUrl,
  type CachedRawObject,
} from "@/lib/cache/object-cache";

interface ObjectsTabProps {
  bucket: BucketItem;
}

type FileItem = {
  name: string;      // Tên hiển thị (ví dụ: "logo.png" hoặc "assets")
  fullName: string;  // Tên full key (ví dụ: "assets/images/logo.png")
  type: "folder" | "file";
  size?: string;
  lastModified?: string;
};

interface RawObject {
  key: string;
  size: number;
  last_modified: string;
}

export function ObjectsTab({ bucket }: ObjectsTabProps) {
  const { subscribeToEvent } = useRealtime();
  const [currentPath, setCurrentPath] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [allObjects, setAllObjects] = useState<RawObject[]>([]);
  const [selectedItems, setSelectedItems] = useState<string[]>([]); // Lưu trữ fullName (full key)
  const fileInputRef = useRef<HTMLInputElement>(null);

  // [COMMENT]: Khởi chạy luồng xin cấp danh sách file từ Dataplane qua Outbox Job và lắng nghe Centrifugo
  const fetchListObjects = async () => {
    setLoading(true);
    try {
      // 1. Kiểm tra cache trước — nếu còn hạn thì render ngay, không cần gọi pipeline
      const cached = getCachedObjectList(bucket.id);
      if (cached) {
        setAllObjects(cached as CachedRawObject[]);
        setSelectedItems([]);
        setLoading(false);
        return;
      }

      // 2. Cache miss — đăng ký xin list job qua Outbox pipeline
      const { event_id } = await registerObjectPresignRequest(bucket.id, bucket.name, "list");

      // 3. Đăng ký Websocket listener bắt sự kiện job hoàn thành
      const unsubscribe = subscribeToEvent("job.notification", (eventData: any) => {
        if (eventData.transaction_id === event_id) {
          if (eventData.status === "SUCCESS") {
            try {
              // Parse danh sách JSON thô nhận trực tiếp qua Centrifugo
              const rawList: RawObject[] = JSON.parse(eventData.message);
              setAllObjects(rawList);
              setSelectedItems([]);
              // [COMMENT]: Lưu kết quả vào cache để tái sử dụng trong 14 phút tiếp theo
              setCachedObjectList(bucket.id, rawList);
            } catch (err) {
              console.error("Failed to parse objects list:", err);
              toast.error("Failed to parse files catalog payload");
            }
          } else {
            toast.error(eventData.message || "Failed to load directory items");
          }
          setLoading(false);
          unsubscribe();
        }
      });

      // Tự động ngắt subscription sau 20s phòng trường hợp lỗi mạng
      setTimeout(() => {
        unsubscribe();
        setLoading((curr) => {
          if (curr) {
            toast.error("Files listing request timed out. Please retry.");
            return false;
          }
          return curr;
        });
      }, 20000);

    } catch (err: any) {
      console.error(err);
      toast.error(err.message || "Failed to start files sync pipeline");
      setLoading(false);
    }
  };

  // Tự động load khi mount bucket
  useEffect(() => {
    fetchListObjects();
  }, [bucket.id]);

  // [COMMENT]: Thuật toán Client-side filtering phân tích cây thư mục ảo tức thì trên RAM
  const getItemsInPath = (rawList: RawObject[], path: string[]): FileItem[] => {
    const prefix = path.length > 0 ? path.join("/") + "/" : "";
    const itemsMap = new Map<string, FileItem>();

    for (const obj of rawList) {
      if (!obj.key.startsWith(prefix)) continue;
      const relativeKey = obj.key.substring(prefix.length);
      if (relativeKey === "") continue; // skip folder placeholder thô nếu có

      const slashIndex = relativeKey.indexOf("/");
      if (slashIndex === -1) {
        // Tệp tin nằm trực tiếp trong thư mục hiện tại
        itemsMap.set(relativeKey, {
          name: relativeKey,
          fullName: obj.key,
          type: "file",
          size: formatBytes(obj.size),
          lastModified: formatDate(obj.last_modified),
        });
      } else {
        // Thư mục con ảo
        const folderName = relativeKey.substring(0, slashIndex);
        if (!itemsMap.has(folderName)) {
          itemsMap.set(folderName, {
            name: folderName,
            fullName: prefix + folderName,
            type: "folder",
          });
        }
      }
    }
    return Array.from(itemsMap.values());
  };

  const formatBytes = (bytes: number): string => {
    if (bytes === 0) return "0 Bytes";
    const k = 1024;
    const sizes = ["Bytes", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + " " + sizes[i];
  };

  const formatDate = (dateStr: string): string => {
    if (!dateStr) return "—";
    try {
      const d = new Date(dateStr);
      return d.toISOString().replace("T", " ").substring(0, 19);
    } catch {
      return dateStr;
    }
  };

  const items = getItemsInPath(allObjects, currentPath);

  const handleFolderClick = (folderName: string) => {
    setCurrentPath([...currentPath, folderName]);
    setSelectedItems([]);
  };

  const handleBreadcrumbClick = (index: number) => {
    if (index === -1) {
      setCurrentPath([]);
    } else {
      setCurrentPath(currentPath.slice(0, index + 1));
    }
    setSelectedItems([]);
  };

  const toggleSelect = (fullName: string) => {
    if (selectedItems.includes(fullName)) {
      setSelectedItems(selectedItems.filter((i) => i !== fullName));
    } else {
      setSelectedItems([...selectedItems, fullName]);
    }
  };

  // [COMMENT]: Xử lý upload file: Xin cấp link upload (PUT), upload trực tiếp lên Envoy rồi sync list
  const handleFileChange = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;

    const key = currentPath.length > 0
      ? currentPath.join("/") + "/" + file.name
      : file.name;

    const toastId = toast.loading(`Requesting upload URL for '${file.name}'...`);

    try {
      // 1. Đăng ký xin link upload
      const { event_id } = await registerObjectPresignRequest(bucket.id, bucket.name, "upload", key, file.type);

      // 2. Chờ Websocket push Presigned PUT URL
      const unsubscribe = subscribeToEvent("job.notification", async (eventData: any) => {
        if (eventData.transaction_id === event_id) {
          unsubscribe();
          if (eventData.status === "SUCCESS") {
            const presignedUrl = eventData.message;
            toast.loading("Transferring file directly to storage gateway...", { id: toastId });

            try {
              // 3. PUT file thô trực tiếp lên Envoy
              const response = await fetch(presignedUrl, {
                method: "PUT",
                body: file,
                headers: {
                  "Content-Type": file.type,
                },
              });

              if (response.ok) {
                toast.success("File uploaded successfully!", { id: toastId });
                // [COMMENT]: Invalidate cache trước khi reload để đảm bảo fetch fresh data từ Dataplane
                invalidateObjectListCache(bucket.id);
                fetchListObjects();
              } else {
                toast.error(`Upload failed: S3 gateway returned status ${response.status}`, { id: toastId });
              }
            } catch (err: any) {
              toast.error("Network upload error: " + err.message, { id: toastId });
            }
          } else {
            toast.error(eventData.message || "Failed to generate S3 upload token", { id: toastId });
          }
        }
      });

    } catch (err: any) {
      toast.error(err.message || "Failed to register upload job", { id: toastId });
    }
  };

  const handleUploadClick = () => {
    fileInputRef.current?.click();
  };

  // [COMMENT]: Xử lý download: Xin cấp GET presigned URL và kích hoạt trigger tải về
  const handleDownload = async () => {
    if (selectedItems.length === 0) return;

    for (const fullName of selectedItems) {
      const toastId = toast.loading(`Preparing download link for ${fullName.split('/').pop()}...`);
      try {
        // [COMMENT]: Kiểm tra cache presign URL trước khi gọi pipeline
        const cachedUrl = getCachedPresignUrl(bucket.id, fullName);
        if (cachedUrl) {
          toast.dismiss(toastId);
          window.open(cachedUrl, "_blank");
          continue;
        }

        const { event_id } = await registerObjectPresignRequest(bucket.id, bucket.name, "download", fullName);

        const unsubscribe = subscribeToEvent("job.notification", (eventData: any) => {
          if (eventData.transaction_id === event_id) {
            unsubscribe();
            if (eventData.status === "SUCCESS") {
              toast.dismiss(toastId);
              const presignedUrl = eventData.message;
              // [COMMENT]: Lưu URL vào cache để tái sử dụng trong TTL còn lại (14 phút)
              setCachedPresignUrl(bucket.id, fullName, presignedUrl);
              window.open(presignedUrl, "_blank");
            } else {
              toast.error(`Failed to sign link: ${eventData.message}`, { id: toastId });
            }
          }
        });
      } catch (err: any) {
        toast.error(err.message || "Download request failed", { id: toastId });
      }
    }
  };

  // [COMMENT]: Xử lý xóa: Xin cấp DELETE presigned URL và gửi yêu cầu xóa qua Envoy
  const handleDelete = async () => {
    if (selectedItems.length === 0) return;
    if (!confirm(`Are you sure you want to delete ${selectedItems.length} selected item(s)?`)) return;

    const toastId = toast.loading(`Initiating deletion for ${selectedItems.length} item(s)...`);

    for (const fullName of selectedItems) {
      try {
        const { event_id } = await registerObjectPresignRequest(bucket.id, bucket.name, "delete", fullName);

        const unsubscribe = subscribeToEvent("job.notification", async (eventData: any) => {
          if (eventData.transaction_id === event_id) {
            unsubscribe();
            if (eventData.status === "SUCCESS") {
              const presignedUrl = eventData.message;
              try {
                const response = await fetch(presignedUrl, { method: "DELETE" });
                if (response.ok) {
                  toast.success("Items deleted successfully from bucket", { id: toastId });
                  // [COMMENT]: Invalidate cache trước khi reload để đảm bảo fetch fresh data từ Dataplane
                  invalidateObjectListCache(bucket.id);
                  fetchListObjects();
                } else {
                  toast.error(`Delete failed at gateway: ${response.status}`, { id: toastId });
                }
              } catch (err: any) {
                toast.error("Network delete error: " + err.message, { id: toastId });
              }
            } else {
              toast.error(eventData.message || "Failed to sign delete command", { id: toastId });
            }
          }
        });
      } catch (err: any) {
        toast.error(err.message || "Deletion request failed", { id: toastId });
      }
    }
  };

  const getIcon = (item: FileItem) => {
    if (item.type === "folder") {
      return <Folder className="h-4.5 w-4.5 text-blue-500 fill-blue-500/10 shrink-0" />;
    }
    if (item.name.endsWith(".png") || item.name.endsWith(".jpg") || item.name.endsWith(".jpeg")) {
      return <Image className="h-4.5 w-4.5 text-emerald-500 shrink-0" />;
    }
    if (item.name.endsWith(".json") || item.name.endsWith(".md") || item.name.endsWith(".sql")) {
      return <FileText className="h-4.5 w-4.5 text-amber-500 shrink-0" />;
    }
    return <File className="h-4.5 w-4.5 text-slate-400 shrink-0" />;
  };

  return (
    <div className="space-y-4 text-xs py-4 select-none">
      <input
        type="file"
        ref={fileInputRef}
        className="hidden"
        onChange={handleFileChange}
      />

      {/* File Browser Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3.5">

        {/* Navigation Breadcrumb */}
        <div className="flex items-center gap-1 text-[13px] font-semibold text-foreground overflow-x-auto py-1">
          <button
            onClick={() => handleBreadcrumbClick(-1)}
            className="flex items-center gap-1 text-slate-500 hover:text-foreground cursor-pointer outline-none"
          >
            <HardDrive className="h-4 w-4" />
            <span>{bucket.name}</span>
          </button>

          {currentPath.map((folder, index) => (
            <React.Fragment key={index}>
              <ChevronRight className="h-3.5 w-3.5 text-slate-400" />
              <button
                onClick={() => handleBreadcrumbClick(index)}
                className="hover:text-foreground cursor-pointer outline-none max-w-[120px] truncate"
              >
                {folder}
              </button>
            </React.Fragment>
          ))}
        </div>

        {/* Action Controls */}
        <div className="flex items-center gap-2">

          {/* Refresh */}
          <Button
            variant="outline"
            onClick={fetchListObjects}
            disabled={loading}
            className="h-8.5 text-xs font-bold border-border text-foreground hover:bg-muted cursor-pointer transition-colors"
          >
            <RefreshCw className={loading ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} />
          </Button>

          {/* Download & Delete Actions */}
          {selectedItems.length > 0 && (
            <>
              <Button
                variant="outline"
                onClick={handleDownload}
                className="h-8.5 text-xs font-bold border-border text-foreground hover:bg-muted cursor-pointer transition-colors flex items-center gap-1.5"
              >
                <Download className="h-3.5 w-3.5" />
                <span>Download ({selectedItems.length})</span>
              </Button>

              <Button
                variant="outline"
                onClick={handleDelete}
                className="h-8.5 text-xs font-bold border-red-200 dark:border-red-950 text-red-655 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-950/20 cursor-pointer transition-colors flex items-center gap-1.5"
              >
                <Trash2 className="h-3.5 w-3.5" />
                <span>Delete</span>
              </Button>
            </>
          )}

          {/* Upload Button */}
          <Button
            onClick={handleUploadClick}
            className="h-8.5 text-xs font-bold bg-blue-600 hover:bg-blue-700 text-white rounded-md flex items-center gap-1.5 cursor-pointer"
          >
            <Upload className="h-3.5 w-3.5" />
            <span>Upload File</span>
          </Button>
        </div>

      </div>

      {/* Object Catalog List */}
      {loading ? (
        <div className="flex flex-col items-center justify-center py-20 text-muted-foreground">
          <Loader2 className="h-7 w-7 animate-spin text-blue-500 mb-2.5" />
          <span className="text-[11px] font-semibold tracking-wider">Loading Files...</span>
        </div>
      ) : items.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-16 text-center text-muted-foreground bg-muted/5 border border-border border-dashed rounded-xl">
          <Folder className="h-10 w-10 text-muted-foreground/50 mb-2.5" />
          <p className="font-bold text-sm">Empty Folder</p>
          <p className="text-[11px] mt-1 max-w-xs text-muted-foreground">
            This directory does not contain any objects. Upload files to get started.
          </p>
        </div>
      ) : (
        <div className="rounded-xl border border-border overflow-hidden">
          <table className="w-full text-left border-collapse table-auto">
            <thead>
              <tr className="border-b border-border bg-muted/20 text-[10px] font-extrabold uppercase tracking-wider text-muted-foreground select-none">
                <th className="w-12 px-6 py-3.5 text-center">
                  <input
                    type="checkbox"
                    checked={selectedItems.length === items.filter(x => x.type === "file").length && items.filter(x => x.type === "file").length > 0}
                    onChange={(e) => {
                      if (e.target.checked) {
                        setSelectedItems(items.filter(x => x.type === "file").map(x => x.fullName));
                      } else {
                        setSelectedItems([]);
                      }
                    }}
                    className="h-3.5 w-3.5 rounded border-border text-blue-600 focus:ring-blue-500 cursor-pointer"
                  />
                </th>
                <th className="px-4 py-3.5">Name</th>
                <th className="px-6 py-3.5">Size</th>
                <th className="px-6 py-3.5">Last Modified</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border text-[13px]">
              {items.map((item) => {
                const isSelected = selectedItems.includes(item.fullName);
                const isFolder = item.type === "folder";
                return (
                  <tr
                    key={item.fullName}
                    className={cn(
                      "hover:bg-muted/40 transition-colors select-none",
                      isSelected && "bg-muted/80"
                    )}
                  >

                    {/* Checkbox */}
                    <td className="px-6 py-3.5 text-center">
                      {!isFolder && (
                        <input
                          type="checkbox"
                          checked={isSelected}
                          onChange={() => toggleSelect(item.fullName)}
                          className="h-3.5 w-3.5 rounded border-border text-blue-600 focus:ring-blue-500 cursor-pointer"
                        />
                      )}
                    </td>

                    {/* Name */}
                    <td className="px-4 py-3.5">
                      <div className="flex items-center gap-2.5">
                        {getIcon(item)}
                        {isFolder ? (
                          <button
                            onClick={() => handleFolderClick(item.name)}
                            className="font-bold text-foreground hover:text-blue-600 dark:hover:text-blue-400 hover:underline cursor-pointer outline-none"
                          >
                            {item.name}
                          </button>
                        ) : (
                          <span className="font-semibold text-slate-700 dark:text-slate-300">
                            {item.name}
                          </span>
                        )}
                      </div>
                    </td>

                    {/* Size */}
                    <td className="px-6 py-3.5 text-slate-700 dark:text-slate-300 font-medium">
                      {isFolder ? "—" : item.size}
                    </td>

                    {/* Last Modified */}
                    <td className="px-6 py-3.5 text-slate-400 dark:text-slate-500">
                      {item.lastModified}
                    </td>

                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

    </div>
  );
}
