import React, { useState } from "react";
import { ShieldAlert, X, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";

interface DeleteKeyModalProps {
  accessKey: string | null;
  onConfirm: () => void;
  onClose: () => void;
  isDeleting: boolean;
}

// [COMMENT]: Modal xác nhận xóa Access Key bằng cách nhập đúng tên Access Key để xác nhận
export function DeleteKeyModal({ accessKey, onConfirm, onClose, isDeleting }: DeleteKeyModalProps) {
  const [confirmName, setConfirmName] = useState("");

  if (!accessKey) return null;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (confirmName !== accessKey) return;
    onConfirm();
    setConfirmName("");
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-xs select-none">
      <div className="w-full max-w-md bg-card text-card-foreground border border-border shadow-lg rounded-xl overflow-hidden animate-in fade-in zoom-in-95 duration-200">

        {/* Modal Header */}
        <div className="flex items-center justify-between px-5 py-4 border-b border-border">
          <div className="flex items-center gap-2">
            <div className="h-7 w-7 flex items-center justify-center rounded-lg bg-red-600/10 text-red-500 border border-red-500/20">
              <ShieldAlert className="h-4 w-4" />
            </div>
            <span className="font-bold text-sm text-foreground">Delete Access Key</span>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground/60 hover:text-foreground cursor-pointer outline-none"
            disabled={isDeleting}
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Modal Form */}
        <form onSubmit={handleSubmit}>
          <div className="p-5 space-y-4">
            <p className="text-xs text-muted-foreground leading-relaxed">
              Are you absolutely sure you want to delete this Access Key? All applications using it will lose access immediately. This action is irreversible.
            </p>
            {/* Input field to verify typing the key name */}
            <div className="flex flex-col gap-1.5">
              <label className="font-bold text-foreground">
                To confirm, type <span className="font-mono font-bold text-red-500 bg-red-500/5 px-1.5 py-0.5 rounded select-all">{accessKey}</span> below:
              </label>
              <input
                type="text"
                placeholder="Type access key here..."
                value={confirmName}
                onChange={(e) => setConfirmName(e.target.value)}
                disabled={isDeleting}
                className="w-full h-9 px-3 bg-background border border-red-500/30 rounded-md text-xs focus:outline-none focus:border-red-500 text-foreground font-mono"
                required
              />
            </div>
          </div>

          {/* Modal Footer Actions */}
          <div className="flex items-center justify-end gap-2 px-5 py-3.5 bg-muted/20 border-t border-border">
            <Button
              type="button"
              variant="outline"
              onClick={onClose}
              disabled={isDeleting}
              className="h-8 text-xs font-bold border-border text-foreground hover:bg-muted"
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={isDeleting || confirmName !== accessKey}
              className="h-8 bg-red-600 hover:bg-red-750 text-white rounded-md font-bold flex items-center gap-1.5 disabled:opacity-50"
            >
              {isDeleting && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              <span>Delete Key</span>
            </Button>
          </div>
        </form>

      </div>
    </div>
  );
}
