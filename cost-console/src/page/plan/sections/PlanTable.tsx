import { Search, Server, MapPin } from "lucide-react";
import { cn } from "../../../lib/utils";

export interface PlanItem {
  id: string;
  name: string;
  code: string;
  resourceType: "storage" | "compute" | "database" | "network";
  zone: "vn-n1" | "vn-n2" | "vn-n3" | "global";
  unit: string;
  priceVnd: number;
  status: "active" | "inactive";
  effectiveFrom: string;
  description: string;
}

interface PlanTableProps {
  plans: PlanItem[];
  selectedPlan: PlanItem | null;
  onSelectPlan: (plan: PlanItem) => void;
  searchTerm: string;
  setSearchTerm: (term: string) => void;
  selectedZone: string;
  setSelectedZone: (zone: string) => void;
  selectedType: string;
  setSelectedType: (type: string) => void;
}

export function PlanTable({
  plans,
  selectedPlan,
  onSelectPlan,
  searchTerm,
  setSearchTerm,
  selectedZone,
  setSelectedZone,
  selectedType,
  setSelectedType,
}: PlanTableProps) {
  // Filters
  const filteredPlans = plans.filter((plan) => {
    const matchesSearch =
      plan.name.toLowerCase().includes(searchTerm.toLowerCase()) ||
      plan.code.toLowerCase().includes(searchTerm.toLowerCase());
    const matchesZone = selectedZone === "all" || plan.zone === selectedZone;
    const matchesType = selectedType === "all" || plan.resourceType === selectedType;
    return matchesSearch && matchesZone && matchesType;
  });

  const getResourceTypeLabel = (type: string) => {
    switch (type) {
      case "storage": return "Storage";
      case "compute": return "Compute";
      case "database": return "Database";
      case "network": return "Network";
      default: return type;
    }
  };

  const getResourceTypeColor = (type: string) => {
    switch (type) {
      case "storage": return "text-emerald-600 dark:text-emerald-400 bg-emerald-50 dark:bg-emerald-950/20";
      case "compute": return "text-blue-600 dark:text-blue-400 bg-blue-50 dark:bg-blue-950/20";
      case "database": return "text-amber-600 dark:text-amber-400 bg-amber-50 dark:bg-amber-950/20";
      case "network": return "text-purple-600 dark:text-purple-400 bg-purple-50 dark:bg-purple-950/20";
      default: return "";
    }
  };

  return (
    <div className="space-y-4">
      {/* Filter and Search Bar */}
      <div className="flex flex-wrap items-center gap-3 pb-4 border-b border-slate-200 dark:border-slate-800">
        {/* Search */}
        <div className="flex items-center bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 px-3 py-1.5 rounded-md w-64 text-xs">
          <Search size={14} className="text-slate-400 mr-2 shrink-0" />
          <input
            type="text"
            placeholder="Tìm theo tên hoặc mã gói cước..."
            value={searchTerm}
            onChange={(e) => setSearchTerm(e.target.value)}
            className="bg-transparent border-none outline-none w-full text-slate-700 dark:text-slate-200 placeholder-slate-400"
          />
        </div>

        {/* Zone Filter */}
        <div className="flex items-center gap-1.5 bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 px-2.5 py-1.5 rounded-md text-xs">
          <MapPin size={13} className="text-slate-400" />
          <select
            value={selectedZone}
            onChange={(e) => setSelectedZone(e.target.value)}
            className="bg-transparent border-none outline-none text-slate-600 dark:text-slate-300 font-semibold cursor-pointer"
          >
            <option value="all">Tất cả Zone</option>
            <option value="vn-n1">Zone vn-n1</option>
            <option value="vn-n2">Zone vn-n2</option>
            <option value="vn-n3">Zone vn-n3</option>
            <option value="global">Global</option>
          </select>
        </div>

        {/* Resource Type Filter */}
        <div className="flex items-center gap-1.5 bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 px-2.5 py-1.5 rounded-md text-xs">
          <Server size={13} className="text-slate-400" />
          <select
            value={selectedType}
            onChange={(e) => setSelectedType(e.target.value)}
            className="bg-transparent border-none outline-none text-slate-600 dark:text-slate-300 font-semibold cursor-pointer"
          >
            <option value="all">Tất cả Dịch vụ</option>
            <option value="storage">Storage</option>
            <option value="compute">Compute</option>
            <option value="database">Database</option>
            <option value="network">Network</option>
          </select>
        </div>
      </div>

      {/* Table Container */}
      <div className="rounded-lg border border-slate-200 dark:border-slate-800 overflow-hidden bg-white dark:bg-slate-900">
        <table className="w-full text-left border-collapse table-auto text-[13px]">
          <thead>
            <tr className="border-b border-slate-200 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-900/30 text-[11px] font-extrabold uppercase tracking-wider text-slate-400 select-none">
              <th className="px-5 py-3">Mã gói</th>
              <th className="px-5 py-3">Tên gói cước</th>
              <th className="px-5 py-3">Dịch vụ</th>
              <th className="px-5 py-3">Zone</th>
              <th className="px-5 py-3 text-right">Đơn giá (VND)</th>
              <th className="px-5 py-3 text-center">Trạng thái</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800/80">
            {filteredPlans.length === 0 ? (
              <tr>
                <td colSpan={6} className="px-5 py-12 text-center text-slate-400 italic">
                  Không tìm thấy gói cước phù hợp
                </td>
              </tr>
            ) : (
              filteredPlans.map((plan) => {
                const isSelected = selectedPlan?.id === plan.id;
                return (
                  <tr
                    key={plan.id}
                    onClick={() => onSelectPlan(plan)}
                    className={cn(
                      "hover:bg-slate-50 dark:hover:bg-slate-800/40 cursor-pointer transition-colors duration-150",
                      isSelected && "bg-blue-50/50 dark:bg-blue-950/20"
                    )}
                  >
                    <td className="px-5 py-3.5 font-mono text-[11px] font-bold text-slate-500">
                      {plan.code}
                    </td>
                    <td className="px-5 py-3.5 font-semibold text-slate-900 dark:text-slate-100">
                      {plan.name}
                    </td>
                    <td className="px-5 py-3.5">
                      <span className={cn(
                        "inline-flex items-center px-2 py-0.5 rounded text-[10px] font-bold uppercase tracking-wide",
                        getResourceTypeColor(plan.resourceType)
                      )}>
                        {getResourceTypeLabel(plan.resourceType)}
                      </span>
                    </td>
                    <td className="px-5 py-3.5 font-semibold text-slate-600 dark:text-slate-300">
                      {plan.zone.toUpperCase()}
                    </td>
                    <td className="px-5 py-3.5 text-right font-mono font-bold text-slate-900 dark:text-slate-100">
                      {plan.priceVnd.toLocaleString("vi-VN")} ₫<span className="text-[10px] text-slate-400 font-normal"> / {plan.unit}</span>
                    </td>
                    <td className="px-5 py-3.5 text-center">
                      <div className="flex justify-center">
                        {plan.status === "active" ? (
                          <span className="flex items-center gap-1 text-[11px] text-emerald-600 dark:text-emerald-400 font-semibold">
                            <span className="w-1.5 h-1.5 rounded-full bg-emerald-500"></span>
                            Active
                          </span>
                        ) : (
                          <span className="flex items-center gap-1 text-[11px] text-slate-400 dark:text-slate-500 font-semibold">
                            <span className="w-1.5 h-1.5 rounded-full bg-slate-400"></span>
                            Inactive
                          </span>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
