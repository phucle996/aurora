import React, { useState } from "react";
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
import { type BucketItem } from "@/lib/api/storage";

interface ObjectsTabProps {
  bucket: BucketItem;
}

type FileItem = {
  name: string;
  type: "folder" | "file";
  size?: string;
  lastModified?: string;
};

export function ObjectsTab({ bucket }: ObjectsTabProps) {
  const [currentPath, setCurrentPath] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [selectedItems, setSelectedItems] = useState<string[]>([]);

  // Premium static mock items for folder structure
  const mockFiles: Record<string, FileItem[]> = {
    root: [
      { name: "assets", type: "folder", lastModified: "2026-07-10 14:02:11" },
      { name: "backups", type: "folder", lastModified: "2026-07-11 08:15:33" },
      { name: "production_logs.tar.gz", type: "file", size: "234.5 MB", lastModified: "2026-07-11 20:30:12" },
      { name: "README.md", type: "file", size: "1.2 KB", lastModified: "2026-07-11 21:10:05" },
    ],
    "root/assets": [
      { name: "images", type: "folder", lastModified: "2026-07-10 14:02:11" },
      { name: "app_config.json", type: "file", size: "4.8 KB", lastModified: "2026-07-10 13:58:24" },
    ],
    "root/assets/images": [
      { name: "logo.png", type: "file", size: "45.2 KB", lastModified: "2026-07-10 14:00:01" },
      { name: "hero_background.jpg", type: "file", size: "1.4 MB", lastModified: "2026-07-10 14:01:45" },
    ],
    "root/backups": [
      { name: "db_snapshot_20260711.sql", type: "file", size: "12.8 MB", lastModified: "2026-07-11 08:15:33" },
    ],
  };

  const getPathKey = () => {
    if (currentPath.length === 0) return "root";
    return `root/${currentPath.join("/")}`;
  };

  const items = mockFiles[getPathKey()] || [];

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

  const toggleSelect = (name: string) => {
    if (selectedItems.includes(name)) {
      setSelectedItems(selectedItems.filter((i) => i !== name));
    } else {
      setSelectedItems([...selectedItems, name]);
    }
  };

  const handleUpload = () => {
    toast.promise(
      new Promise((resolve) => setTimeout(resolve, 1500)),
      {
        loading: "Uploading mock file to storage cluster...",
        success: "File uploaded successfully!",
        error: "Upload failed",
      }
    );
  };

  const handleDownload = () => {
    if (selectedItems.length === 0) return;
    toast.success(`Initiating download for ${selectedItems.length} selected item(s)`);
  };

  const handleDelete = () => {
    if (selectedItems.length === 0) return;
    if (confirm(`Are you sure you want to delete ${selectedItems.length} item(s)?`)) {
      toast.success(`Deleted ${selectedItems.length} item(s) from bucket`);
      setSelectedItems([]);
    }
  };

  const handleSync = () => {
    setLoading(true);
    setTimeout(() => {
      setLoading(false);
      toast.success("Object browser list refreshed");
    }, 800);
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
      
      {/* File Browser Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border pb-3.5">
        
        {/* Navigation Breadcrumb */}
        <div className="flex items-center gap-1 text-[13px] font-semibold text-foreground overflow-x-auto py-1">
          <button
            onClick={() => handleBreadcrumbClick(-1)}
            className="flex items-center gap-1 text-slate-500 hover:text-foreground cursor-pointer outline-none"
          >
            <HardDrive className="h-4 w-4" />
            <span>{bucket.Name}</span>
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
            onClick={handleSync}
            disabled={loading}
            className="h-8.5 text-xs font-bold border-border text-foreground hover:bg-muted cursor-pointer transition-colors"
          >
            <RefreshCw className={loading ? "h-3.5 w-3.5 animate-spin" : "h-3.5 w-3.5"} />
          </Button>

          {/* Download */}
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
              
              {/* Delete */}
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

          {/* Upload */}
          <Button
            onClick={handleUpload}
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
                        setSelectedItems(items.filter(x => x.type === "file").map(x => x.name));
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
                const isSelected = selectedItems.includes(item.name);
                const isFolder = item.type === "folder";
                return (
                  <tr
                    key={item.name}
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
                          onChange={() => toggleSelect(item.name)}
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
