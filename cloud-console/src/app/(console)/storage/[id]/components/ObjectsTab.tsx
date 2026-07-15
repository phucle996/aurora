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
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { toast } from "sonner";
import { cn } from "@/lib/utils";
import { type BucketItem, requestBucketStsToken } from "@/lib/api/storage";
import { useRealtime } from "@/context/RealtimeContext";
import { UploadModal } from "./UploadModal";
import { DeleteConfirmModal } from "./DeleteConfirmModal";
import {
  getCachedObjectList,
  setCachedObjectList,
  invalidateObjectListCache,
  type CachedRawObject,
} from "@/lib/cache/object-cache";
import {
  S3Client,
  ListObjectsV2Command,
  GetObjectCommand,
  DeleteObjectCommand,
  HeadObjectCommand,
  GetObjectTaggingCommand,
} from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";

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

// [COMMENT]: Giải mã thủ công protobuf binary nhận được từ Centrifugo (được hex-encode)
// Tiết kiệm băng thông và tối ưu hóa nhị phân mà không cần thư viện bên thứ ba.
function decodeObjectStsResponse(hexStr: string) {
  const bytes = new Uint8Array(
    hexStr.match(/.{1,2}/g)?.map((byte) => parseInt(byte, 16)) || []
  );

  let offset = 0;
  let access_key = "";
  let secret_key = "";
  let session_token = "";
  let expiration = "";
  let endpoint = "";

  while (offset < bytes.length) {
    const tag = bytes[offset++];
    const fieldNum = tag >> 3;
    const wireType = tag & 0x07;

    if (wireType === 2) {
      // Đọc độ dài dạng varint
      let len = 0;
      let shift = 0;
      while (true) {
        const b = bytes[offset++];
        len |= (b & 0x7f) << shift;
        if ((b & 0x80) === 0) break;
        shift += 7;
      }
      // Trích xuất chuỗi UTF-8 tương ứng
      const strBytes = bytes.slice(offset, offset + len);
      offset += len;
      const val = new TextDecoder().decode(strBytes);

      if (fieldNum === 1) access_key = val;
      else if (fieldNum === 2) secret_key = val;
      else if (fieldNum === 3) session_token = val;
      else if (fieldNum === 4) expiration = val;
      else if (fieldNum === 5) endpoint = val;
    } else {
      // Trường hợp wire type khác thì bỏ qua
      break;
    }
  }

  return { access_key, secret_key, session_token, expiration, endpoint };
}

export function ObjectsTab({ bucket }: ObjectsTabProps) {
  const { subscribeToEvent } = useRealtime();
  const [currentPath, setCurrentPath] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [allObjects, setAllObjects] = useState<RawObject[]>([]);
  const [selectedItems, setSelectedItems] = useState<string[]>([]); // Lưu trữ fullName (full key)
  
  const [uploadModalOpen, setUploadModalOpen] = useState(false);
  const [deleteConfirmOpen, setDeleteConfirmOpen] = useState(false);
  const [isDeleting, setIsDeleting] = useState(false);

  // [COMMENT]: State quản lý S3 Client và token hạn dùng
  const [s3Client, setS3Client] = useState<S3Client | null>(null);
  const s3ClientRef = useRef<S3Client | null>(null);
  const expirationRef = useRef<Date | null>(null);

  // [COMMENT]: State quản lý slide-over drawer xem chi tiết metadata
  const [selectedFile, setSelectedFile] = useState<FileItem | null>(null);
  const [metadataLoading, setMetadataLoading] = useState(false);
  const [fileDetails, setFileDetails] = useState<{
    contentType?: string;
    etag?: string;
    customMetadata?: Record<string, string>;
    tags?: Record<string, string>;
    versionId?: string;
  } | null>(null);

  // [COMMENT]: Hàm lấy hoặc khởi tạo S3 client từ STS token (cache trong 30p)
  const getS3Client = async (): Promise<S3Client> => {
    if (s3ClientRef.current && expirationRef.current && expirationRef.current.getTime() > Date.now() + 60000) {
      return s3ClientRef.current;
    }

    const toastId = toast.loading("Đang kết nối cổng lưu trữ bảo mật (STS)...");
    try {
      const { event_id } = await requestBucketStsToken(bucket.id, 1800);

      // Đợi Centrifugo phản hồi kết quả job STS thành công
      const credentials = await new Promise<any>((resolve, reject) => {
        const timeout = setTimeout(() => {
          unsubscribe();
          reject(new Error("Không nhận được phản hồi từ cổng bảo mật (Timeout)."));
        }, 20000);

        const unsubscribe = subscribeToEvent("job.notification", (eventData: any) => {
          if (eventData.transaction_id === event_id) {
            clearTimeout(timeout);
            unsubscribe();
            if (eventData.status === "SUCCESS") {
              try {
                const creds = decodeObjectStsResponse(eventData.message);
                // [COMMENT]: WIPE / Xóa sạch payload khóa trong message truyền dẫn để tránh rò rỉ qua console/memory F12
                eventData.message = "";
                resolve(creds);
              } catch (err) {
                reject(new Error("Lỗi giải mã thông tin xác thực từ Gateway."));
              }
            } else {
              reject(new Error(eventData.message || "Gateway từ chối cấp khóa truy cập."));
            }
          }
        });
      });

      toast.dismiss(toastId);

      const host = typeof window !== "undefined" ? window.location.hostname : "localhost";
      let endpoint = credentials.endpoint || `http://${host}:29000`;
      if (endpoint.includes("localhost") && typeof window !== "undefined") {
        endpoint = endpoint.replace("localhost", window.location.hostname);
      }

      const newClient = new S3Client({
        endpoint,
        region: "us-east-1",
        credentials: {
          accessKeyId: credentials.access_key,
          secretAccessKey: credentials.secret_key,
          sessionToken: credentials.session_token,
        },
        forcePathStyle: true,
      });

      s3ClientRef.current = newClient;
      expirationRef.current = new Date(credentials.expiration);
      setS3Client(newClient);

      return newClient;
    } catch (err: any) {
      toast.error(err.message || "Không thể khởi tạo kết nối S3.", { id: toastId });
      throw err;
    }
  };

  // [COMMENT]: Khởi chạy truy vấn S3 list và nạp danh sách tệp tin
  const fetchListObjects = async (force = false) => {
    if (force) {
      invalidateObjectListCache(bucket.id);
    }

    setLoading(true);
    try {
      const cached = force ? null : getCachedObjectList(bucket.id);
      if (cached) {
        setAllObjects(cached as CachedRawObject[]);
        setSelectedItems([]);
        setLoading(false);
        return;
      }

      const client = await getS3Client();

      let allContents: any[] = [];
      let nextToken: string | undefined = undefined;
      do {
        const command = new ListObjectsV2Command({
          Bucket: bucket.name,
          ContinuationToken: nextToken,
        });
        const res = (await client.send(command)) as any;
        if (res.Contents) {
          allContents.push(...res.Contents);
        }
        nextToken = res.NextContinuationToken;
      } while (nextToken);

      const mapped: RawObject[] = allContents.map((obj) => ({
        key: obj.Key || "",
        size: obj.Size || 0,
        last_modified: obj.LastModified ? obj.LastModified.toISOString() : "",
      }));

      setAllObjects(mapped);
      setSelectedItems([]);
      setCachedObjectList(bucket.id, mapped);
    } catch (err: any) {
      console.error("List objects failed:", err);
    } finally {
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

  const handleUploadClick = () => {
    setUploadModalOpen(true);
  };

  // [COMMENT]: Tải xuống: Ký link GET trực tiếp ở client-side và tải về
  const handleDownload = async () => {
    if (selectedItems.length === 0) return;

    const client = await getS3Client();

    for (let i = 0; i < selectedItems.length; i++) {
      const fullName = selectedItems[i];

      if (i > 0) {
        await new Promise((resolve) => setTimeout(resolve, 400));
      }

      const toastId = toast.loading(`Đang ký link tải về cho ${fullName.split('/').pop()}...`);
      try {
        const command = new GetObjectCommand({
          Bucket: bucket.name,
          Key: fullName,
        });
        const presignedUrl = await getSignedUrl(client, command, { expiresIn: 900 });

        toast.dismiss(toastId);
        
        const link = document.createElement("a");
        link.href = presignedUrl;
        link.target = "_blank";
        link.setAttribute("download", fullName.split('/').pop() || "");
        document.body.appendChild(link);
        link.click();
        document.body.removeChild(link);
      } catch (err: any) {
        toast.error(err.message || "Không thể ký link tải xuống", { id: toastId });
      }
    }
  };

  // [COMMENT]: Xử lý xóa đơn lẻ dùng S3 Command trực tiếp
  const deleteSingleItem = async (fullName: string): Promise<void> => {
    const client = await getS3Client();
    const command = new DeleteObjectCommand({
      Bucket: bucket.name,
      Key: fullName,
    });
    await client.send(command);
  };

  // [COMMENT]: Xóa song song qua Promise.all
  const handleDeleteConfirm = async () => {
    if (selectedItems.length === 0) return;
    setIsDeleting(true);

    const toastId = toast.loading(`Đang xóa ${selectedItems.length} đối tượng...`);
    try {
      await Promise.all(selectedItems.map((item) => deleteSingleItem(item)));

      toast.success(`Đã xóa thành công ${selectedItems.length} đối tượng khỏi bucket!`, { id: toastId });
      setSelectedItems([]);
      setDeleteConfirmOpen(false);
    } catch (err: any) {
      toast.error(err.message || "Xóa một hoặc nhiều đối tượng thất bại.", { id: toastId });
    } finally {
      setIsDeleting(false);
      invalidateObjectListCache(bucket.id);
      fetchListObjects();
    }
  };

  // [COMMENT]: Xem chi tiết metadata tệp tin và nạp tags
  const handleFileClick = async (fileItem: FileItem) => {
    setSelectedFile(fileItem);
    setMetadataLoading(true);
    try {
      const client = await getS3Client();
      
      // 1. Gọi HeadObject lấy System Metadata + Custom Metadata
      const headCommand = new HeadObjectCommand({
        Bucket: bucket.name,
        Key: fileItem.fullName,
      });
      const headRes = await client.send(headCommand);

      // 2. Gọi GetObjectTagging lấy nhãn tags của object
      const taggingCommand = new GetObjectTaggingCommand({
        Bucket: bucket.name,
        Key: fileItem.fullName,
        VersionId: headRes.VersionId, // ID version để tránh race condition nếu tệp bị ghi đè
      });
      const tagRes = await client.send(taggingCommand).catch(() => null);

      const tagsMap: Record<string, string> = {};
      if (tagRes && tagRes.TagSet) {
        for (const t of tagRes.TagSet) {
          if (t.Key && t.Value) {
            tagsMap[t.Key] = t.Value;
          }
        }
      }

      setFileDetails({
        contentType: headRes.ContentType,
        etag: headRes.ETag,
        customMetadata: headRes.Metadata,
        tags: tagsMap,
        versionId: headRes.VersionId,
      });
    } catch (err: any) {
      console.error("Head metadata failed:", err);
      toast.error("Không thể lấy siêu dữ liệu (metadata) của tệp.");
    } finally {
      setMetadataLoading(false);
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
    <div className="space-y-4 text-xs py-4 select-none relative">

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
            onClick={() => fetchListObjects(true)}
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
                onClick={() => setDeleteConfirmOpen(true)}
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
                          <button
                            onClick={() => handleFileClick(item)}
                            className="font-semibold text-slate-700 dark:text-slate-300 hover:text-blue-600 dark:hover:text-blue-400 hover:underline cursor-pointer outline-none text-left"
                          >
                            {item.name}
                          </button>
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

      {/* Slide-over Detail Drawer */}
      {selectedFile && (
        <div className="fixed inset-y-0 right-0 z-50 w-full max-w-md bg-white dark:bg-slate-900 border-l border-slate-200 dark:border-slate-800 shadow-2xl flex flex-col animate-in slide-in-from-right duration-200 ease-out select-text">
          {/* Header */}
          <div className="flex items-center justify-between px-5 py-4 border-b border-slate-100 dark:border-slate-800/80">
            <div className="flex items-center gap-2">
              <FileText className="h-4.5 w-4.5 text-blue-500" />
              <h3 className="text-sm font-bold text-slate-800 dark:text-slate-200 truncate max-w-[280px]">
                Chi tiết đối tượng
              </h3>
            </div>
            <button
              onClick={() => {
                setSelectedFile(null);
                setFileDetails(null);
              }}
              className="p-1 rounded-md hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer outline-none"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          {/* Content */}
          <div className="flex-1 overflow-y-auto p-5 space-y-6">
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
          <div className="p-4 border-t border-slate-100 dark:border-slate-800/80 bg-slate-50/50 dark:bg-slate-900/30 flex items-center justify-between">
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
                  setSelectedFile(null);
                  setFileDetails(null);
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
      )}

      {/* Upload Dialog Modal */}
      <UploadModal
        isOpen={uploadModalOpen}
        onClose={() => setUploadModalOpen(false)}
        bucket={bucket}
        currentPath={currentPath}
        subscribeToEvent={subscribeToEvent}
        fetchListObjects={fetchListObjects}
        getS3Client={getS3Client}
      />

      {/* Delete Confirmation Modal */}
      <DeleteConfirmModal
        isOpen={deleteConfirmOpen}
        onClose={() => setDeleteConfirmOpen(false)}
        onConfirm={handleDeleteConfirm}
        items={selectedItems}
        isDeleting={isDeleting}
      />

    </div>
  );
}
