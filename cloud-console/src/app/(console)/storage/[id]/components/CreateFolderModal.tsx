import React, { useState } from "react";
import { X, FolderPlus, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";

interface CreateFolderModalProps {
  isOpen: boolean;
  onClose: () => void;
  onCreate: (folderName: string) => Promise<void>;
}

export const CreateFolderModal: React.FC<CreateFolderModalProps> = ({
  isOpen,
  onClose,
  onCreate,
}) => {
  const [folderName, setFolderName] = useState("");
  const [isCreating, setIsCreating] = useState(false);

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!folderName.trim()) return;
    setIsCreating(true);
    try {
      await onCreate(folderName.trim());
      setFolderName("");
      onClose();
    } catch (err) {
      // Error is caught/handled in caller
    } finally {
      setIsCreating(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center text-xs">
      {/* Backdrop */}
      <div
        onClick={isCreating ? undefined : onClose}
        className="absolute inset-0 bg-slate-950/40 dark:bg-slate-950/60 backdrop-blur-sm cursor-pointer"
      />

      {/* Modal Dialog Content */}
      <form
        onSubmit={handleSubmit}
        className="relative w-full max-w-sm bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl shadow-2xl overflow-hidden flex flex-col z-10 animate-in fade-in zoom-in-95 duration-200"
      >
        {/* Header */}
        <div className="px-5 py-4 border-b border-slate-100 dark:border-slate-800 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <div className="p-1 bg-blue-50 dark:bg-blue-950/30 rounded-lg">
              <FolderPlus className="h-4.5 w-4.5 text-blue-500" />
            </div>
            <h3 className="text-sm font-extrabold text-slate-800 dark:text-slate-100">
              Tạo thư mục mới
            </h3>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={isCreating}
            className="p-1 rounded-md hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer disabled:opacity-50 transition-colors outline-none"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Body Content */}
        <div className="px-6 py-5 flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <label className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
              Tên thư mục
            </label>
            <input
              type="text"
              autoFocus
              value={folderName}
              onChange={(e) => setFolderName(e.target.value)}
              placeholder="ví dụ: assets, images, backups..."
              disabled={isCreating}
              className="px-3.5 py-2.5 rounded-lg border border-slate-200 dark:border-slate-800 bg-transparent text-slate-800 dark:text-slate-200 placeholder:text-slate-400 focus:outline-none focus:ring-1 focus:ring-blue-500 focus:border-blue-500 font-medium"
            />
          </div>
        </div>

        {/* Footer Actions */}
        <div className="px-6 py-4 border-t border-slate-100 dark:border-slate-800/80 bg-slate-50/50 dark:bg-slate-900/30 flex items-center justify-end gap-2.5">
          <Button
            type="button"
            variant="outline"
            disabled={isCreating}
            onClick={onClose}
            className="h-8.5 text-[11px] font-bold bg-white hover:bg-slate-50 border-slate-200 dark:border-slate-800 dark:bg-slate-900 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-300 cursor-pointer transition-colors"
          >
            Hủy bỏ
          </Button>
          <Button
            type="submit"
            disabled={isCreating || !folderName.trim()}
            className="h-8.5 text-[11px] font-bold bg-blue-600 hover:bg-blue-700 text-white cursor-pointer transition-colors flex items-center gap-1.5"
          >
            {isCreating ? (
              <>
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                <span>Đang tạo...</span>
              </>
            ) : (
              <span>Tạo thư mục</span>
            )}
          </Button>
        </div>
      </form>
    </div>
  );
};
