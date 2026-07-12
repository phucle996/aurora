import React from "react";
import { FileCode, X } from "lucide-react";
import { Button } from "@/components/ui/button";

interface PolicyViewModalProps {
  policy: string | null;
  onClose: () => void;
}

// [COMMENT]: Component hiển thị Policy JSON chi tiết dạng Modal Overlay
export function PolicyViewModal({ policy, onClose }: PolicyViewModalProps) {
  if (!policy) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs select-none">
      {/* [COMMENT]: Tăng kích thước max-w từ max-w-lg lên max-w-3xl để phần content rộng hơn */}
      <div className="w-full max-w-3xl bg-card text-card-foreground border border-border shadow-lg rounded-xl overflow-hidden animate-in fade-in zoom-in-95 duration-200">

        {/* Modal Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border">
          <span className="font-bold text-sm text-foreground flex items-center gap-1.5">
            <FileCode className="h-4 w-4 text-blue-500" />
            <span>Access Policy JSON</span>
          </span>
          <button
            onClick={onClose}
            className="text-muted-foreground/60 hover:text-foreground cursor-pointer outline-none"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Modal Body: Hiển thị mã policy mà không bị cuộn ngang bằng whitespace-pre-wrap */}
        <div className="p-5">
          <pre className="p-4 bg-slate-950 text-slate-100 rounded-md font-mono text-[11px] whitespace-pre-wrap break-all overflow-y-auto max-h-[500px]">
            {policy}
          </pre>
        </div>

        {/* Modal Footer */}
        <div className="flex items-center justify-end px-5 py-3.5 bg-muted/20 border-t border-border">
          <Button
            type="button"
            onClick={onClose}
            className="h-8.5 text-xs font-bold bg-slate-800 hover:bg-slate-700 text-white rounded-md cursor-pointer transition-colors"
          >
            Close
          </Button>
        </div>

      </div>
    </div>
  );
}
