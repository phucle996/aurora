import { useState } from "react";
import { Plus, Coins, Info } from "lucide-react";
import { PlanTable, type PlanItem } from "./sections/PlanTable";
import { PlanDetail } from "./sections/PlanDetail";
import { PlanModal } from "./sections/PlanModal";
import { cn } from "../../lib/utils";

const INITIAL_PLANS: PlanItem[] = [
  {
    id: "plan-1",
    name: "Standard Block Storage",
    code: "STORAGE_GB_MONTH_VN1",
    resourceType: "storage",
    zone: "vn-n1",
    unit: "GB/Month",
    priceVnd: 500,
    status: "active",
    effectiveFrom: "2026-07-01",
    description: "Định giá cho dung lượng lưu trữ khối tiêu chuẩn tại Zone vn-n1.",
  },
  {
    id: "plan-2",
    name: "High Performance NVMe Storage",
    code: "STORAGE_NVME_GB_VN1",
    resourceType: "storage",
    zone: "vn-n1",
    unit: "GB/Month",
    priceVnd: 1200,
    status: "active",
    effectiveFrom: "2026-07-01",
    description: "Định giá lưu trữ NVMe tốc độ cao, độ trễ cực thấp cho cơ sở dữ liệu lớn.",
  },
  {
    id: "plan-3",
    name: "Standard Block Storage (vn-n2)",
    code: "STORAGE_GB_MONTH_VN2",
    resourceType: "storage",
    zone: "vn-n2",
    unit: "GB/Month",
    priceVnd: 450,
    status: "active",
    effectiveFrom: "2026-07-10",
    description: "Định giá lưu trữ tiêu chuẩn áp dụng riêng cho hạ tầng thuộc Zone vn-n2.",
  },
  {
    id: "plan-4",
    name: "Standard Compute vCPU Core",
    code: "VM_CORE_HOUR_VN1",
    resourceType: "compute",
    zone: "vn-n1",
    unit: "vCPU/Hour",
    priceVnd: 200,
    status: "active",
    effectiveFrom: "2026-07-01",
    description: "Giá cước tính theo giờ cho mỗi lõi vCPU máy ảo tiêu chuẩn tại Zone vn-n1.",
  },
  {
    id: "plan-5",
    name: "Standard Compute vCPU Core (vn-n3)",
    code: "VM_CORE_HOUR_VN3",
    resourceType: "compute",
    zone: "vn-n3",
    unit: "vCPU/Hour",
    priceVnd: 180,
    status: "active",
    effectiveFrom: "2026-07-15",
    description: "Giá cước tính theo giờ cho mỗi vCPU máy ảo tại cụm Zone vn-n3.",
  },
  {
    id: "plan-6",
    name: "PostgreSQL Dedicated Instance",
    code: "DB_PG_INSTANCE_VN1",
    resourceType: "database",
    zone: "vn-n1",
    unit: "Instance/Hour",
    priceVnd: 2500,
    status: "active",
    effectiveFrom: "2026-07-05",
    description: "Phí vận hành máy chủ cơ sở dữ liệu PostgreSQL chuyên dụng theo giờ.",
  },
  {
    id: "plan-7",
    name: "Network Egress Data Transfer",
    code: "NET_EGRESS_GB_GLOBAL",
    resourceType: "network",
    zone: "global",
    unit: "GB",
    priceVnd: 1000,
    status: "active",
    effectiveFrom: "2026-07-01",
    description: "Cước truyền dữ liệu ra Internet (Egress) toàn cầu từ toàn bộ các cụm zone.",
  },
];

export default function PlanPage() {
  const [plans, setPlans] = useState<PlanItem[]>(INITIAL_PLANS);
  const [selectedPlan, setSelectedPlan] = useState<PlanItem | null>(null);

  // Filter states
  const [searchTerm, setSearchTerm] = useState("");
  const [selectedZone, setSelectedZone] = useState("all");
  const [selectedType, setSelectedType] = useState("all");

  // Modal states
  const [modalOpen, setModalOpen] = useState(false);
  const [editPlan, setEditPlan] = useState<PlanItem | null>(null);

  const handleSavePlan = async (
    data: Omit<PlanItem, "id" | "status" | "effectiveFrom"> & { id?: string }
  ) => {
    if (data.id) {
      // Edit
      setPlans((prev) =>
        prev.map((plan) =>
          plan.id === data.id
            ? {
                ...plan,
                name: data.name,
                code: data.code,
                resourceType: data.resourceType,
                zone: data.zone,
                unit: data.unit,
                priceVnd: data.priceVnd,
                description: data.description,
              }
            : plan
        )
      );
      // Update selected detail if currently selected
      if (selectedPlan && selectedPlan.id === data.id) {
        setSelectedPlan((prev) =>
          prev
            ? {
                ...prev,
                name: data.name,
                code: data.code,
                resourceType: data.resourceType,
                zone: data.zone,
                unit: data.unit,
                priceVnd: data.priceVnd,
                description: data.description,
              }
            : null
        );
      }
    } else {
      // Create new
      const newPlan: PlanItem = {
        id: `plan-${Date.now()}`,
        name: data.name,
        code: data.code,
        resourceType: data.resourceType,
        zone: data.zone,
        unit: data.unit,
        priceVnd: data.priceVnd,
        status: "active",
        effectiveFrom: new Date().toISOString().split("T")[0],
        description: data.description,
      };
      setPlans((prev) => [newPlan, ...prev]);
    }
  };

  const handleToggleStatus = (plan: PlanItem) => {
    const updatedStatus = plan.status === "active" ? "inactive" : "active";
    setPlans((prev) =>
      prev.map((p) => (p.id === plan.id ? { ...p, status: updatedStatus } : p))
    );
    setSelectedPlan((prev) => (prev && prev.id === plan.id ? { ...prev, status: updatedStatus } : prev));
  };

  const handleCreateClick = () => {
    setEditPlan(null);
    setModalOpen(true);
  };

  const handleEditClick = (plan: PlanItem) => {
    setEditPlan(plan);
    setModalOpen(true);
  };

  return (
    <div className="flex flex-col lg:flex-row gap-6 w-full relative items-stretch select-none">
      {/* Left Column (Master List) */}
      <div className={cn(
        "space-y-4 transition-all duration-300 ease-in-out",
        selectedPlan ? "w-full lg:w-[67%]" : "w-full lg:w-full"
      )}>
        {/* Sub-Header Actions */}
        <div className="flex justify-between items-center pb-1.5 select-none">
          <div className="flex items-center gap-2">
            <Coins className="h-5 w-5 text-blue-500" />
            <div>
              <p className="text-[11px] text-slate-500 font-bold uppercase tracking-wider">
                Cost & Billing Control Plane
              </p>
            </div>
          </div>
          <button
            onClick={handleCreateClick}
            className="flex items-center gap-1.5 bg-blue-600 hover:bg-blue-700 text-white font-bold text-xs px-3.5 py-2 rounded-md shadow-sm transition-colors cursor-pointer"
          >
            <Plus size={14} />
            <span>Tạo gói cước mới</span>
          </button>
        </div>

        {/* Informative banner */}
        <div className="p-3 bg-slate-50 dark:bg-slate-900/40 border border-slate-200 dark:border-slate-800 rounded-lg flex items-start gap-2.5">
          <Info className="h-4 w-4 text-blue-500 shrink-0 mt-0.5" />
          <div className="text-[11px] text-slate-500 dark:text-slate-400 leading-relaxed">
            Danh sách đơn giá dịch vụ dùng để đo kiểm lưu lượng thực tế và khấu trừ tiền trong ví của khách hàng tự động. Hãy chọn một gói cước để xem cấu hình chi tiết và các thuộc tính liên quan.
          </div>
        </div>

        {/* Plan Table Component */}
        <PlanTable
          plans={plans}
          selectedPlan={selectedPlan}
          onSelectPlan={setSelectedPlan}
          searchTerm={searchTerm}
          setSearchTerm={setSearchTerm}
          selectedZone={selectedZone}
          setSelectedZone={setSelectedZone}
          selectedType={selectedType}
          setSelectedType={setSelectedType}
        />
      </div>

      {/* Right Column (Detail Panel) */}
      {selectedPlan && (
        <PlanDetail
          plan={selectedPlan}
          onClose={() => setSelectedPlan(null)}
          onEdit={handleEditClick}
          onToggleStatus={handleToggleStatus}
        />
      )}

      {/* Plan Creation / Edit Modal */}
      <PlanModal
        isOpen={modalOpen}
        onClose={() => setModalOpen(false)}
        onSave={handleSavePlan}
        editPlan={editPlan}
      />
    </div>
  );
}
