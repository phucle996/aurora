import { useState, useEffect } from "react";
import { X, Coins, Loader2 } from "lucide-react";
import { type PlanItem } from "./PlanTable";
import { type ZoneItem } from "../../../lib/api/billing";

interface PlanModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSave: (plan: Omit<PlanItem, "id" | "status" | "effectiveFrom"> & { id?: string }) => Promise<void>;
  editPlan: PlanItem | null;
  zones: ZoneItem[];
}

export function PlanModal({ isOpen, onClose, onSave, editPlan, zones }: PlanModalProps) {
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [resourceType, setResourceType] = useState<"storage" | "compute" | "database" | "network">("storage");
  const [zone, setZone] = useState("");
  const [unit, setUnit] = useState("GB/Month");
  const [priceVnd, setPriceVnd] = useState<number>(0);
  const [description, setDescription] = useState("");
  const [isSaving, setIsSaving] = useState(false);

  useEffect(() => {
    if (editPlan) {
      setName(editPlan.name);
      setCode(editPlan.code);
      setResourceType(editPlan.resourceType);
      setZone(editPlan.zone);
      setUnit(editPlan.unit);
      setPriceVnd(editPlan.priceVnd);
      setDescription(editPlan.description);
    } else {
      setName("");
      setCode("");
      setResourceType("storage");
      setZone(zones[0]?.code.toLowerCase() || "vn-n1");
      setUnit("GB/Month");
      setPriceVnd(0);
      setDescription("");
    }
  }, [editPlan, isOpen, zones]);

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || !code.trim() || priceVnd <= 0) return;
    setIsSaving(true);
    try {
      await onSave({
        id: editPlan?.id,
        name: name.trim(),
        code: code.trim().toUpperCase(),
        resourceType,
        zone,
        unit: unit.trim(),
        priceVnd,
        description: description.trim(),
      });
      onClose();
    } catch (err) {
      // Error is handled in page component
    } finally {
      setIsSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center text-xs">
      {/* Backdrop */}
      <div
        onClick={isSaving ? undefined : onClose}
        className="absolute inset-0 bg-slate-950/40 dark:bg-slate-950/60 backdrop-blur-sm cursor-pointer"
      />

      {/* Modal Dialog Content */}
      <form
        onSubmit={handleSubmit}
        className="relative w-full max-w-md bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl shadow-2xl overflow-hidden flex flex-col z-10 animate-in fade-in zoom-in-95 duration-200"
      >
        {/* Header */}
        <div className="px-5 py-4 border-b border-slate-100 dark:border-slate-800 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <div className="p-1 bg-blue-50 dark:bg-blue-950/30 rounded-lg">
              <Coins className="h-4.5 w-4.5 text-blue-500" />
            </div>
            <h3 className="text-sm font-extrabold text-slate-800 dark:text-slate-100">
              {editPlan ? "Cập nhật cấu hình gói cước" : "Tạo gói cước mới"}
            </h3>
          </div>
          <button
            type="button"
            onClick={onClose}
            disabled={isSaving}
            className="p-1 rounded-md hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer disabled:opacity-50 transition-colors outline-none"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Body Content */}
        <div className="px-6 py-5 flex flex-col gap-4 max-h-[70vh] overflow-y-auto">
          {/* Plan Name */}
          <div className="flex flex-col gap-1">
            <label className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
              Tên gói cước
            </label>
            <input
              type="text"
              required
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="ví dụ: Standard Block Storage, Premium Core Compute..."
              disabled={isSaving}
              className="px-3 py-2 rounded border border-slate-200 dark:border-slate-800 bg-transparent text-slate-800 dark:text-slate-200 placeholder:text-slate-400 focus:outline-none focus:ring-1 focus:ring-blue-500 font-medium text-xs"
            />
          </div>

          {/* Plan Code */}
          <div className="flex flex-col gap-1">
            <label className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
              Mã gói cước (Code)
            </label>
            <input
              type="text"
              required
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="ví dụ: STORAGE_GB_MONTH, VM_CORE_HOUR"
              disabled={isSaving || !!editPlan}
              className="px-3 py-2 rounded border border-slate-200 dark:border-slate-800 bg-transparent text-slate-800 dark:text-slate-200 placeholder:text-slate-400 focus:outline-none focus:ring-1 focus:ring-blue-500 font-mono text-xs uppercase"
            />
          </div>

          {/* Resource Type & Zone */}
          <div className="grid grid-cols-2 gap-4">
            <div className="flex flex-col gap-1">
              <label className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
                Loại dịch vụ
              </label>
              <select
                value={resourceType}
                onChange={(e) => setResourceType(e.target.value as any)}
                disabled={isSaving}
                className="px-3 py-2 rounded border border-slate-200 dark:border-slate-800 bg-transparent text-slate-800 dark:text-slate-200 focus:outline-none focus:ring-1 focus:ring-blue-500 font-medium text-xs"
              >
                <option value="storage">Storage</option>
                <option value="compute">Compute</option>
                <option value="database">Database</option>
                <option value="network">Network</option>
              </select>
            </div>

            <div className="flex flex-col gap-1">
              <label className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
                Zone hạ tầng
              </label>
              <select
                value={zone}
                onChange={(e) => setZone(e.target.value)}
                disabled={isSaving}
                className="px-3 py-2 rounded border border-slate-200 dark:border-slate-800 bg-transparent text-slate-800 dark:text-slate-200 focus:outline-none focus:ring-1 focus:ring-blue-500 font-medium text-xs"
              >
                {zones.map((z) => (
                  <option key={z.id} value={z.code.toLowerCase()}>
                    Zone {z.name} ({z.code})
                  </option>
                ))}
                <option value="global">Global</option>
              </select>
            </div>
          </div>

          {/* Price & Unit */}
          <div className="grid grid-cols-2 gap-4">
            <div className="flex flex-col gap-1">
              <label className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
                Đơn giá (VND)
              </label>
              <input
                type="number"
                required
                min={1}
                value={priceVnd || ""}
                onChange={(e) => setPriceVnd(Number(e.target.value))}
                placeholder="ví dụ: 500, 1000"
                disabled={isSaving}
                className="px-3 py-2 rounded border border-slate-200 dark:border-slate-800 bg-transparent text-slate-800 dark:text-slate-200 placeholder:text-slate-400 focus:outline-none focus:ring-1 focus:ring-blue-500 font-medium text-xs"
              />
            </div>

            <div className="flex flex-col gap-1">
              <label className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
                Đơn vị tính (Unit)
              </label>
              <input
                type="text"
                required
                value={unit}
                onChange={(e) => setUnit(e.target.value)}
                placeholder="ví dụ: GB/Tháng, Core/Giờ"
                disabled={isSaving}
                className="px-3 py-2 rounded border border-slate-200 dark:border-slate-800 bg-transparent text-slate-800 dark:text-slate-200 placeholder:text-slate-400 focus:outline-none focus:ring-1 focus:ring-blue-500 font-medium text-xs"
              />
            </div>
          </div>

          {/* Description */}
          <div className="flex flex-col gap-1">
            <label className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
              Mô tả chi tiết
            </label>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Nhập thông tin chi tiết về gói cước..."
              rows={3}
              disabled={isSaving}
              className="px-3 py-2 rounded border border-slate-200 dark:border-slate-800 bg-transparent text-slate-800 dark:text-slate-200 placeholder:text-slate-400 focus:outline-none focus:ring-1 focus:ring-blue-500 font-medium text-xs resize-none"
            />
          </div>
        </div>

        {/* Footer Actions */}
        <div className="px-6 py-4 border-t border-slate-100 dark:border-slate-800/80 bg-slate-50/50 dark:bg-slate-900/30 flex items-center justify-end gap-2.5 select-none">
          <button
            type="button"
            disabled={isSaving}
            onClick={onClose}
            className="h-8.5 px-3.5 rounded bg-white hover:bg-slate-50 border border-slate-200 dark:bg-slate-900 dark:border-slate-800 dark:hover:bg-slate-800 text-slate-700 dark:text-slate-300 font-bold cursor-pointer transition-colors"
          >
            Hủy bỏ
          </button>
          <button
            type="submit"
            disabled={isSaving || !name.trim() || !code.trim() || priceVnd <= 0}
            className="h-8.5 px-3.5 rounded bg-blue-600 hover:bg-blue-700 text-white font-bold cursor-pointer transition-colors flex items-center gap-1.5"
          >
            {isSaving ? (
              <>
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                <span>Đang lưu...</span>
              </>
            ) : (
              <span>{editPlan ? "Lưu thay đổi" : "Tạo gói cước"}</span>
            )}
          </button>
        </div>
      </form>
    </div>
  );
}
