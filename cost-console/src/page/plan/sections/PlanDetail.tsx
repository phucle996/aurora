import { X, Calendar, Coins, ShieldAlert } from "lucide-react";
import { cn } from "../../../lib/utils";
import { type PlanItem } from "./PlanTable";

interface PlanDetailProps {
  plan: PlanItem;
  onClose: () => void;
  onEdit: (plan: PlanItem) => void;
  onToggleStatus: (plan: PlanItem) => void;
}

export function PlanDetail({ plan, onClose, onEdit, onToggleStatus }: PlanDetailProps) {
  const getResourceTypeLabel = (type: string) => {
    switch (type) {
      case "storage": return "Storage Bucket / S3 Storage";
      case "compute": return "Virtual Machine / Compute Core";
      case "database": return "PostgreSQL / ClickHouse Cluster";
      case "network": return "Egress Traffic / Network bandwidth";
      default: return type;
    }
  };

  return (
    <div className="w-full lg:w-[33%] bg-transparent pl-6 lg:border-l border-slate-200 dark:border-slate-800/80 flex flex-col gap-5 animate-in slide-in-from-right duration-300 ease-in-out select-text text-xs">
      {/* Header */}
      <div className="flex items-center justify-between pb-3.5 border-b border-slate-200 dark:border-slate-800/80 select-none">
        <div className="flex items-center gap-2">
          <Coins className="h-4.5 w-4.5 text-blue-500" />
          <h3 className="text-sm font-bold text-slate-800 dark:text-slate-200">
            Chi tiết cấu hình giá
          </h3>
        </div>
        <button
          onClick={onClose}
          className="p-1 rounded-md hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-400 hover:text-slate-600 dark:hover:text-slate-200 cursor-pointer transition-colors outline-none"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      {/* Content */}
      <div className="space-y-5">
        {/* Overview Row */}
        <div className="bg-slate-50 dark:bg-slate-900/40 p-4 rounded-lg border border-slate-100 dark:border-slate-800/50 space-y-3">
          <div>
            <span className="block text-[9px] uppercase tracking-wider text-slate-400 font-bold">Mã gói cước</span>
            <span className="font-mono text-xs font-bold text-slate-700 dark:text-slate-200">{plan.code}</span>
          </div>
          <div>
            <span className="block text-[9px] uppercase tracking-wider text-slate-400 font-bold">Tên gói cước</span>
            <span className="text-xs font-bold text-slate-900 dark:text-slate-100">{plan.name}</span>
          </div>
        </div>

        {/* Pricing Specification */}
        <div className="border-b border-slate-200 dark:border-slate-800/60 pb-4 space-y-3">
          <h4 className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
            Thông số định giá
          </h4>
          <div className="grid grid-cols-2 gap-4 text-[11px]">
            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400">Đơn giá VND</span>
              <span className="text-slate-800 dark:text-slate-200 font-bold">
                {plan.priceVnd.toLocaleString("vi-VN")} ₫
              </span>
            </div>
            <div>
              <span className="block text-[9px] uppercase tracking-wider text-slate-400">Đơn vị tính</span>
              <span className="text-slate-800 dark:text-slate-200 font-bold">Per {plan.unit}</span>
            </div>
          </div>
        </div>

        {/* Resource Scope */}
        <div className="border-b border-slate-200 dark:border-slate-800/60 pb-4 space-y-3">
          <h4 className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
            Phạm vi áp dụng
          </h4>
          <div className="space-y-2">
            <div className="flex justify-between items-center text-[11px]">
              <span className="text-slate-400">Loại tài nguyên:</span>
              <span className="font-semibold text-slate-700 dark:text-slate-200">
                {getResourceTypeLabel(plan.resourceType)}
              </span>
            </div>
            <div className="flex justify-between items-center text-[11px]">
              <span className="text-slate-400">Zone hạ tầng:</span>
              <span className="font-mono font-bold text-slate-700 dark:text-slate-200 uppercase">
                {plan.zone}
              </span>
            </div>
            <div className="flex justify-between items-center text-[11px]">
              <span className="text-slate-400">Ngày hiệu lực:</span>
              <span className="font-semibold text-slate-700 dark:text-slate-200 flex items-center gap-1">
                <Calendar className="h-3 w-3 text-slate-400" />
                {plan.effectiveFrom}
              </span>
            </div>
          </div>
        </div>

        {/* Description Section */}
        <div className="border-b border-slate-200 dark:border-slate-800/60 pb-4 space-y-2">
          <h4 className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
            Mô tả gói cước
          </h4>
          <p className="text-[11px] text-slate-500 dark:text-slate-400 leading-relaxed bg-slate-50/50 dark:bg-slate-900/20 p-2.5 border border-slate-100 dark:border-slate-800/60 rounded">
            {plan.description || "Không có mô tả chi tiết cho gói cước này."}
          </p>
        </div>

        {/* Audit Details */}
        <div className="pb-2 space-y-2">
          <h4 className="text-[10px] font-bold text-slate-400 dark:text-slate-500 uppercase tracking-wider">
            Chính sách khấu hao & kiểm toán
          </h4>
          <div className="p-3 bg-blue-50/30 dark:bg-blue-950/10 border border-blue-100/50 dark:border-blue-900/30 rounded-lg flex items-start gap-2.5">
            <ShieldAlert className="h-4 w-4 text-blue-500 shrink-0 mt-0.5" />
            <div className="text-[10px] text-slate-500 dark:text-slate-400 leading-relaxed">
              Đơn giá này sẽ được áp dụng trực tiếp cho các luồng xử lý của <strong>cost-manager-engine</strong> để tính cước thời gian thực dựa trên logs từ <strong>VictoriaLogs</strong> và dữ liệu đo kiểm.
            </div>
          </div>
        </div>
      </div>

      {/* Footer Actions */}
      <div className="pt-4 border-t border-slate-200 dark:border-slate-800/80 flex items-center justify-between gap-2.5 select-none mt-auto">
        <button
          onClick={() => onToggleStatus(plan)}
          className={cn(
            "h-8 px-3 rounded-md text-[11px] font-bold cursor-pointer transition-colors flex-1 flex items-center justify-center border",
            plan.status === "active"
              ? "bg-white hover:bg-slate-50 border-slate-200 text-slate-700 dark:bg-slate-900 dark:border-slate-800 dark:hover:bg-slate-800 dark:text-slate-300"
              : "bg-emerald-600 hover:bg-emerald-700 border-transparent text-white"
          )}
        >
          {plan.status === "active" ? "Tạm ngưng gói" : "Kích hoạt gói"}
        </button>
        <button
          onClick={() => onEdit(plan)}
          className="h-8 px-3 rounded-md text-[11px] font-bold bg-blue-600 hover:bg-blue-700 text-white cursor-pointer transition-colors flex-1 flex items-center justify-center"
        >
          Cập nhật cấu hình
        </button>
      </div>
    </div>
  );
}
