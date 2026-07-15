import React from "react";
import { X, AlertTriangle, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";

interface DeleteConfirmModalProps {
  isOpen: boolean;
  onClose: () => void;
  onConfirm: () => void;
  items: string[];
  isDeleting: boolean;
}

export const DeleteConfirmModal: React.FC<DeleteConfirmModalProps> = ({
  isOpen,
  onClose,
  onConfirm,
  items,
  isDeleting,
}) => {
  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div
        onClick={isDeleting ? undefined : onClose}
        className="absolute inset-0 bg-slate-950/40 dark:bg-slate-950/60 backdrop-blur-sm cursor-pointer"
      />

      {/* Modal Dialog Content */}
      <div className="relative w-full max-w-md bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl shadow-2xl overflow-hidden flex flex-col z-10 animate-in fade-in zoom-in-95 duration-200">

        {/* Header */}
        <div className="px-5 py-4 border-b border-slate-100 dark:border-slate-800 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <div className="p-1 bg-red-50 dark:bg-red-950/30 rounded-lg">
              <AlertTriangle className="h-4.5 w-4.5 text-red-500" />
            </div>
            <h3 className="text-sm font-extrabold text-slate-800 dark:text-slate-100">
              Xác nhận xóa đối tượng
            </h3>
          </div>
          <button
            onClick={onClose}
            disabled={isDeleting}
            className="p-1 rounded-md hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer disabled:opacity-50 transition-colors"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Body Content */}
        <div className="px-6 py-5 flex flex-col gap-4">
          <p className="text-xs text-slate-500 dark:text-slate-400 leading-relaxed">
            Bạn có chắc chắn muốn xóa vĩnh viễn <span className="font-extrabold text-red-500">{items.length}</span> đối tượng đã chọn dưới đây? Hành động này sẽ xóa hoàn toàn dữ liệu trên MinIO/S3 và <span className="font-extrabold text-red-500">không thể phục hồi</span>.
          </p>

          {/* List of files with scrollbar if items are numerous */}
          <div className="border border-slate-100 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-900/30 rounded-lg max-h-[160px] overflow-y-auto p-3.5 space-y-2">
            {items.map((item, index) => {
              const fileName = item.split("/").pop() || item;
              return (
                <div key={index} className="flex items-center gap-2 text-slate-600 dark:text-slate-300">
                  <div className="h-1.5 w-1.5 rounded-full bg-red-400 dark:bg-red-500 shrink-0" />
                  <span className="text-[11px] font-mono truncate" title={item}>
                    {fileName}
                  </span>
                </div>
              );
            })}
          </div>
        </div>

        {/* Footer Actions */}
        <div className="px-5 py-3.5 border-t border-slate-100 dark:border-slate-800 bg-slate-50/40 dark:bg-slate-900/10 flex justify-end gap-2.5">
          <Button
            variant="outline"
            onClick={onClose}
            disabled={isDeleting}
            className="h-8.5 text-xs font-bold border-border text-foreground hover:bg-muted cursor-pointer transition-colors"
          >
            Hủy bỏ
          </Button>
          <Button
            variant="destructive"
            onClick={onConfirm}
            disabled={isDeleting}
            className="h-8.5 text-xs font-bold bg-red-600 dark:bg-red-500 text-white hover:bg-red-700 dark:hover:bg-red-600 cursor-pointer disabled:opacity-50 transition-colors flex items-center gap-1.5"
          >
            {isDeleting ? (
              <>
                <svg className="animate-spin -ml-0.5 mr-1.5 h-3.5 w-3.5 text-white" fill="none" viewBox="0 0 24 24">
                  <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                  <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
                </svg>
                <span>Đang xóa...</span>
              </>
            ) : (
              <>
                <Trash2 className="h-3.5 w-3.5" />
                <span>Xác nhận xóa</span>
              </>
            )}
          </Button>
        </div>
      </div>
    </div>
  );
};
