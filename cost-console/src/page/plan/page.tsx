import { useState, useEffect } from "react";
import { useSearchParams } from "react-router-dom"; // Hook quản lý URL query search parameters
import { Plus, Coins, Info, RefreshCw, MapPin } from "lucide-react";
import { PlanTable, type PlanItem } from "./sections/PlanTable";
import { PlanDetail } from "./sections/PlanDetail";
import { PlanModal } from "./sections/PlanModal";
import { SubscriptionPanel } from "./sections/SubscriptionPanel";
import { TierTable } from "./sections/TierTable"; // [COMMENT]: Import thêm component bảng cước lũy tiến Tiers mới
import { billingApi, type Subscription, type ZoneItem, type PriceItem } from "../../lib/api/billing";
import { cn } from "../../lib/utils";

const mapPlanResponseToItem = (p: any): PlanItem => ({
  id: p.id,
  name: p.name,
  code: p.code,
  resourceType: (p.service_type?.toLowerCase() || 'storage') as any,
  zone: (p.zone_id?.toLowerCase() || 'global') as any,
  unit: p.metrics?.[0]?.unit || 'Month',
  priceVnd: p.monthly_price,
  status: p.status === 'ACTIVE' ? 'active' : 'inactive',
  effectiveFrom: p.created_at ? new Date(p.created_at).toISOString().split('T')[0] : new Date().toISOString().split('T')[0],
  description: p.description || '',
});

export default function PlanPage() {
  const [plans, setPlans] = useState<PlanItem[]>([]);
  const [zones, setZones] = useState<ZoneItem[]>([]);
  const [prices, setPrices] = useState<PriceItem[]>([]);

  // Sử dụng useSearchParams để đọc và ghi query parameter ?tab= trên URL
  const [searchParams, setSearchParams] = useSearchParams();

  // Xác định tab hiện tại từ URL query param, nếu không có hoặc giá trị lạ thì mặc định là 'plans'
  // [COMMENT]: Đổi cấu hình tab từ 'pricing' (Biểu giá cũ) thành 'tiers' (Biểu giá lũy tiến mới)
  const tabParam = searchParams.get("tab");
  const activeTab: "plans" | "tiers" | "subscriptions" =
    tabParam === "tiers" || tabParam === "subscriptions" ? tabParam : "plans";

  // Hàm xử lý chuyển tab và cập nhật URL query string ?tab=...
  const handleTabChange = (newTab: "plans" | "tiers" | "subscriptions") => {
    setSearchParams({ tab: newTab });
  };

  const [loading, setLoading] = useState(true);
  const [selectedPlan, setSelectedPlan] = useState<PlanItem | null>(null);
  const [activeSub, setActiveSub] = useState<Subscription | null>(null);
  const [refreshTrigger, setRefreshTrigger] = useState(0);

  // Filter states
  const [searchTerm, setSearchTerm] = useState("");
  const [selectedZone, setSelectedZone] = useState("all");
  const [selectedType, setSelectedType] = useState("all");

  // Modal states
  const [modalOpen, setModalOpen] = useState(false);
  const [editPlan, setEditPlan] = useState<PlanItem | null>(null);

  // Fetch plans, zones, prices and current subscription status
  const loadData = async () => {
    setLoading(true);
    try {
      const planList = await billingApi.listPlans();
      setPlans(planList.map(mapPlanResponseToItem));
      const sub = await billingApi.getActiveSubscription();
      setActiveSub(sub);
      const zoneList = await billingApi.listZones();
      setZones(zoneList);
      const priceList = await billingApi.listPrices();
      setPrices(priceList);
    } catch (err) {
      console.error("Failed to load billing page data:", err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadData();
  }, [refreshTrigger]);

  const handleSavePlan = async (
    data: Omit<PlanItem, "id" | "status" | "effectiveFrom"> & { id?: string }
  ) => {
    try {
      if (data.id) {
        alert("Tính năng sửa gói cước đang được cập nhật!");
      } else {
        // Create new plan on backend with standard storage metrics structure
        const newPlan = await billingApi.createPlan({
          name: data.name,
          code: data.code,
          service_type: data.resourceType.toUpperCase(),
          zone_id: data.zone,
          monthly_price: data.priceVnd,
          currency: "VND",
          description: data.description,
          metrics: [
            {
              id: "",
              plan_id: "",
              metric_type: "STORAGE_AT_REST",
              quota: 100, // Default 100 GB limit
              unit: "GB"
            },
            {
              id: "",
              plan_id: "",
              metric_type: "EGRESS_INTERNET",
              quota: 20, // Default 20 GB egress
              unit: "GB"
            }
          ] as any
        });
        setPlans((prev) => [mapPlanResponseToItem(newPlan), ...prev]);
        setRefreshTrigger((prev) => prev + 1);
      }
    } catch (err: any) {
      alert("Lỗi khi lưu gói cước: " + err.message);
    }
  };

  const handleToggleStatus = async (plan: PlanItem) => {
    const nextStatus = plan.status === "active" ? "DEPRECATED" : "ACTIVE";
    try {
      await billingApi.updatePlanStatus(plan.id, nextStatus);
      setPlans((prev) =>
        prev.map((p) => (p.id === plan.id ? { ...p, status: nextStatus === "ACTIVE" ? "active" : "inactive" } : p))
      );
      setSelectedPlan((prev) =>
        prev && prev.id === plan.id ? { ...prev, status: nextStatus === "ACTIVE" ? "active" : "inactive" } : prev
      );
    } catch (err: any) {
      alert("Lỗi khi đổi trạng thái: " + err.message);
    }
  };

  const handleSubscribe = async (plan: PlanItem) => {
    if (!window.confirm(`Bạn có chắc chắn muốn đăng ký gói cước "${plan.name}" với giá ${plan.priceVnd.toLocaleString("vi-VN")} ₫/tháng?`)) {
      return;
    }
    try {
      await billingApi.subscribe(plan.id);
      alert("Đăng ký gói cước thành công!");
      setRefreshTrigger((prev) => prev + 1);
    } catch (err: any) {
      alert("Đăng ký gói thất bại: " + err.message);
    }
  };

  const handleCreateClick = () => {
    setEditPlan(null);
    setModalOpen(true);
  };

  const handleEditClick = (plan: PlanItem) => {
    setEditPlan(plan);
    setModalOpen(true);
  };

  const renderPricingTab = () => {
    return (
      <div className="space-y-4">
        <div className="p-3 bg-slate-50 dark:bg-slate-900/40 border border-slate-200 dark:border-slate-800 rounded-lg flex items-start gap-2.5">
          <Info className="h-4 w-4 text-blue-500 shrink-0 mt-0.5" />
          <div className="text-[11px] text-slate-500 dark:text-slate-400 leading-relaxed">
            Biểu giá dịch vụ Pay-as-you-go được áp dụng khi tài nguyên tiêu thụ vượt hạn mức của gói thuê bao đăng ký, hoặc khi khách hàng không đăng ký gói cước nào. Giá tính theo giờ tích lũy thực tế.
          </div>
        </div>

        <div className="overflow-x-auto border border-slate-200 dark:border-slate-800 rounded-xl bg-white dark:bg-slate-900">
          <table className="w-full text-left text-xs border-collapse">
            <thead>
              <tr className="bg-slate-50 dark:bg-slate-900 border-b border-slate-200 dark:border-slate-800 text-[10px] font-bold text-slate-400 uppercase tracking-wider select-none">
                <th className="px-5 py-3.5">Dịch vụ</th>
                <th className="px-5 py-3.5">Metric & Chiều đo</th>
                <th className="px-5 py-3.5 text-center">Zone</th>
                <th className="px-5 py-3.5">Hạn mức miễn phí</th>
                <th className="px-5 py-3.5">Tier lưu trữ</th>
                <th className="px-5 py-3.5 text-right">Đơn giá VND</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-200 dark:divide-slate-800">
              {prices.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-5 py-12 text-center text-slate-400 font-medium">
                    Không tìm thấy cấu hình giá dịch vụ nào.
                  </td>
                </tr>
              ) : (
                prices.map((p) => (
                  <tr key={p.id} className="hover:bg-slate-50/50 dark:hover:bg-slate-800/20 transition-colors">
                    <td className="px-5 py-3.5 font-bold text-slate-800 dark:text-slate-100 uppercase tracking-wide">
                      {p.service_type}
                    </td>
                    <td className="px-5 py-3.5">
                      <div className="flex flex-col">
                        <span className="font-semibold text-slate-700 dark:text-slate-300">{p.metric_type}</span>
                        <span className="text-[10px] text-slate-400 mt-0.5">Đơn vị tính: {p.unit}</span>
                      </div>
                    </td>
                    <td className="px-5 py-3.5 text-center">
                      <span className="inline-flex items-center gap-1 bg-slate-100 dark:bg-slate-800 px-2 py-0.5 rounded text-[10px] font-bold text-slate-600 dark:text-slate-400 capitalize">
                        <MapPin size={10} />
                        {p.zone_id}
                      </span>
                    </td>
                    <td className="px-5 py-3.5 font-semibold text-slate-600 dark:text-slate-400">
                      {p.free_quota > 0 ? `${p.free_quota} ${p.unit.replace("_HOUR", "")}` : "Không có"}
                    </td>
                    <td className="px-5 py-3.5">
                      <span className="inline-flex items-center bg-blue-50 dark:bg-blue-950/30 text-blue-600 dark:text-blue-400 px-2 py-0.5 rounded text-[10px] font-bold uppercase tracking-wider">
                        {p.tier}
                      </span>
                    </td>
                    <td className="px-5 py-3.5 text-right font-mono font-bold text-blue-600 dark:text-blue-400 text-sm">
                      {p.unit_price.toLocaleString("vi-VN")} ₫ <span className="text-[10px] text-slate-400 font-sans font-normal">/{p.unit.toLowerCase()}</span>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
    );
  };

  return (
    <div className="space-y-6 select-none">
      {/* Sub-Header Tabs */}
      <div className="flex justify-between items-center pb-1.5 select-none border-b border-slate-200 dark:border-slate-800">
        <div className="flex items-center gap-6">
          <div className="flex items-center gap-2 border-r border-slate-200 dark:border-slate-800 pr-4">
            <Coins className="h-5 w-5 text-blue-500" />
            <div>
              <p className="text-[11px] text-slate-500 font-bold uppercase tracking-wider">
                Cost & Billing Control Plane
              </p>
            </div>
          </div>

          <div className="flex gap-2">
            {/* Tab Gói Cước -> ?tab=plans */}
            <button
              onClick={() => handleTabChange("plans")}
              className={cn(
                "px-3 py-1.5 font-bold text-xs rounded-md transition-colors cursor-pointer outline-none",
                activeTab === "plans"
                  ? "bg-slate-100 dark:bg-slate-800 text-slate-800 dark:text-slate-100"
                  : "text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-300"
              )}
            >
              Gói Cước (Plans)
            </button>
            {/* [COMMENT]: Đổi tab Biểu Giá cũ thành Biểu Giá Gốc (Tiers) tương ứng với cấu trúc mới */}
            <button
              onClick={() => handleTabChange("tiers")}
              className={cn(
                "px-3 py-1.5 font-bold text-xs rounded-md transition-colors cursor-pointer outline-none",
                activeTab === "tiers"
                  ? "bg-slate-100 dark:bg-slate-800 text-slate-800 dark:text-slate-100"
                  : "text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-300"
              )}
            >
              Biểu Giá Gốc (Tiers)
            </button>
            {/* Tab Gói Đăng Ký -> ?tab=subscriptions */}
            <button
              onClick={() => handleTabChange("subscriptions")}
              className={cn(
                "px-3 py-1.5 font-bold text-xs rounded-md transition-colors cursor-pointer outline-none",
                activeTab === "subscriptions"
                  ? "bg-slate-100 dark:bg-slate-800 text-slate-800 dark:text-slate-100"
                  : "text-slate-500 hover:text-slate-700 dark:text-slate-400 dark:hover:text-slate-300"
              )}
            >
              Gói Đăng Ký (Subscriptions)
            </button>
          </div>
        </div>

        {activeTab === "plans" && (
          <button
            onClick={handleCreateClick}
            className="flex items-center gap-1.5 bg-blue-600 hover:bg-blue-700 text-white font-bold text-xs px-3.5 py-2 rounded-md shadow-sm transition-colors cursor-pointer"
          >
            <Plus size={14} />
            <span>Tạo gói cước mới</span>
          </button>
        )}
      </div>

      {/* Tab Contents */}
      {activeTab === "plans" && (
        <div className="flex flex-col lg:flex-row gap-6 w-full relative items-stretch">
          {/* Left Column (Master List) */}
          <div className={cn(
            "space-y-4 transition-all duration-300 ease-in-out",
            selectedPlan ? "w-full lg:w-[67%]" : "w-full lg:w-full"
          )}>
            <div className="p-3 bg-slate-50 dark:bg-slate-900/40 border border-slate-200 dark:border-slate-800 rounded-lg flex items-start gap-2.5">
              <Info className="h-4 w-4 text-blue-500 shrink-0 mt-0.5" />
              <div className="text-[11px] text-slate-500 dark:text-slate-400 leading-relaxed">
                Danh sách các gói cước trọn gói hàng tháng được tối ưu hóa cho từng đối tượng sử dụng. Hãy chọn một gói cước để xem cấu hình chi tiết và các thuộc tính liên quan.
              </div>
            </div>

            {loading && plans.length === 0 ? (
              <div className="p-12 text-center border border-slate-200 dark:border-slate-800 border-dashed rounded-xl bg-white dark:bg-slate-900 text-slate-400 text-xs flex items-center justify-center gap-2">
                <RefreshCw className="animate-spin" size={16} />
                Đang tải danh sách gói cước...
              </div>
            ) : (
              <PlanTable
                plans={plans}
                zones={zones}
                selectedPlan={selectedPlan}
                onSelectPlan={setSelectedPlan}
                searchTerm={searchTerm}
                setSearchTerm={setSearchTerm}
                selectedZone={selectedZone}
                setSelectedZone={setSelectedZone}
                selectedType={selectedType}
                setSelectedType={setSelectedType}
              />
            )}
          </div>

          {/* Right Column (Detail Panel) */}
          {selectedPlan && (
            <PlanDetail
              plan={selectedPlan}
              onClose={() => setSelectedPlan(null)}
              onEdit={handleEditClick}
              onToggleStatus={handleToggleStatus}
              isSubscribed={activeSub?.plan_id === selectedPlan.id}
              onSubscribe={handleSubscribe}
            />
          )}

          {/* Plan Creation / Edit Modal */}
          <PlanModal
            isOpen={modalOpen}
            onClose={() => setModalOpen(false)}
            onSave={handleSavePlan}
            editPlan={editPlan}
            zones={zones}
          />
        </div>
      )}

      {/* [COMMENT]: Render bảng Tiers mới khi activeTab là 'tiers' */}
      {activeTab === "tiers" && <TierTable />}

      {activeTab === "subscriptions" && (
        <div className="space-y-4">
          <SubscriptionPanel
            refreshTrigger={refreshTrigger}
            onSubscribeSuccess={() => setRefreshTrigger((prev) => prev + 1)}
          />
        </div>
      )}
    </div>
  );
}
