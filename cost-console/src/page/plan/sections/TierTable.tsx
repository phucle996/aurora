import { useState, useEffect } from "react";
import { Search, Layers, ChevronLeft, ChevronRight } from "lucide-react";
import { billingApi, type TierItem } from "../../../lib/api/billing";
import { cn } from "../../../lib/utils";

// [COMMENT]: Định dạng phân tách dấu phẩy cho số nguyên để hiển thị MB trực quan (VD: 51200 -> 51,200)
function formatNumber(num: number): string {
  return num.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

export function TierTable() {
  const [tiers, setTiers] = useState<TierItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const limit = 10; // Số bản ghi trên mỗi trang (tăng lên 10 vì hiển thị dạng phẳng chiếm ít dòng hơn)

  // [COMMENT]: Trạng thái bộ lọc tìm kiếm và loại dịch vụ
  const [searchTerm, setSearchTerm] = useState("");
  const [serviceType, setServiceType] = useState("all");
  const [loading, setLoading] = useState(false);

  // [COMMENT]: Hàm fetch danh sách Tiers từ API backend
  const fetchTiers = async () => {
    setLoading(true);
    try {
      const res = await billingApi.listTiers(page, limit, serviceType, searchTerm);
      setTiers(res.tiers || []);
      setTotal(res.pagination?.total || 0);
    } catch (e) {
      console.error("Failed to load tiers list:", e);
    } finally {
      setLoading(false);
    }
  };

  // Tải lại dữ liệu khi đổi trang hoặc thay đổi bộ lọc
  useEffect(() => {
    fetchTiers();
  }, [page, serviceType, searchTerm]);

  // Reset về trang 1 khi đổi bộ lọc
  const handleFilterChange = (type: string) => {
    setServiceType(type);
    setPage(1);
  };

  const handleSearchChange = (term: string) => {
    setSearchTerm(term);
    setPage(1);
  };

  const totalPages = Math.ceil(total / limit) || 1;

  const getServiceTypeColor = (type: string) => {
    switch (type) {
      case "STORAGE":
        return "text-emerald-500 bg-emerald-950/20 border border-emerald-500/20";
      case "NETWORK_IN":
        return "text-blue-500 bg-blue-950/20 border border-blue-500/20";
      case "NETWORK_OUT":
        return "text-amber-500 bg-amber-950/20 border border-amber-500/20";
      default:
        return "text-slate-500 bg-slate-950/20 border border-slate-500/20";
    }
  };

  return (
    <div className="space-y-4">
      {/* [COMMENT]: Khung tìm kiếm và bộ lọc Resource Type */}
      <div className="flex flex-wrap items-center gap-3 pb-4 border-b border-slate-800">
        <div className="flex items-center bg-slate-900/60 border border-slate-800 px-3 py-1.5 rounded text-xs w-64">
          <Search size={14} className="text-slate-400 mr-2 shrink-0" />
          <input
            type="text"
            placeholder="Tìm theo tên hoặc mã biểu giá..."
            value={searchTerm}
            onChange={(e) => handleSearchChange(e.target.value)}
            className="bg-transparent border-none outline-none w-full text-slate-200 placeholder-slate-500"
          />
        </div>

        <div className="flex items-center gap-1.5 bg-slate-900/60 border border-slate-800 px-2.5 py-1.5 rounded text-xs">
          <Layers size={13} className="text-slate-400" />
          <select
            value={serviceType}
            onChange={(e) => handleFilterChange(e.target.value)}
            className="bg-transparent border-none outline-none text-slate-300 font-semibold cursor-pointer"
          >
            <option value="all">Tất cả Dịch vụ</option>
            <option value="STORAGE">Storage</option>
            <option value="NETWORK_IN">Inbound Network</option>
            <option value="NETWORK_OUT">Outbound Network</option>
          </select>
        </div>
      </div>

      {/* [COMMENT]: Bảng hiển thị cước phí chi tiết dạng Flat Table */}
      <div className="border border-slate-800 rounded bg-[#080d14] overflow-hidden">
        <table className="w-full text-left border-collapse">
          <thead>
            <tr className="border-b border-slate-800 bg-slate-900/40 text-slate-400 text-[10px] uppercase tracking-wider font-semibold">
              <th className="py-3 px-4 w-1/4">Tên / Mã biểu giá</th>
              <th className="py-3 px-4 w-1/6">Loại tài nguyên</th>
              <th className="py-3 px-4 w-1/4">Định mức bậc cước (Range)</th>
              <th className="py-3 px-4 text-right">Đơn giá cơ sở</th>
              <th className="py-3 px-4 text-right w-1/6">Ngày tạo</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-800/60 text-xs">
            {loading ? (
              <tr>
                <td colSpan={5} className="py-8 text-center text-slate-500">
                  Đang tải danh sách biểu giá...
                </td>
              </tr>
            ) : tiers.length === 0 ? (
              <tr>
                <td colSpan={5} className="py-8 text-center text-slate-500">
                  Không tìm thấy biểu giá nào phù hợp.
                </td>
              </tr>
            ) : (
              tiers.map((tier) => {
                const isUnlimited = tier.range_end === 0;
                const startGb = tier.range_start / 1024;
                const endGb = tier.range_end / 1024;
                const dateStr = tier.created_at ? new Date(tier.created_at).toLocaleDateString("vi-VN") : "-";

                return (
                  <tr key={tier.id} className="hover:bg-slate-900/20 transition-colors">
                    {/* Tên biểu giá */}
                    <td className="py-3.5 px-4">
                      <div className="font-semibold text-slate-200">{tier.name}</div>
                      <div className="text-[10px] text-slate-500 font-mono mt-0.5">{tier.code}</div>
                    </td>
                    {/* Loại tài nguyên */}
                    <td className="py-3.5 px-4">
                      <span className={cn("px-2 py-0.5 rounded text-[10px] font-semibold uppercase", getServiceTypeColor(tier.service_type))}>
                        {tier.service_type}
                      </span>
                    </td>
                    {/* Định mức Range */}
                    <td className="py-3.5 px-4 font-mono text-emerald-400 font-semibold">
                      {isUnlimited ? (
                        `> ${formatNumber(tier.range_start)} MB (>${startGb} GB)`
                      ) : (
                        `${formatNumber(tier.range_start)} - ${formatNumber(tier.range_end)} MB (${startGb} - ${endGb} GB)`
                      )}
                    </td>
                    {/* Đơn giá cơ sở */}
                    <td className="py-3.5 px-4 text-right font-mono text-slate-200">
                      <span className="font-bold">{formatNumber(tier.base_unit_price)}</span>
                      <span className="text-slate-500 text-[10px] ml-1">Micro-units/MB/Giờ</span>
                    </td>
                    {/* Ngày tạo */}
                    <td className="py-3.5 px-4 text-right text-slate-500 font-mono">
                      {dateStr}
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>

      {/* [COMMENT]: Khung phân trang phân cấp mỏng hairline */}
      <div className="flex items-center justify-between pt-2 text-xs text-slate-400">
        <div>
          Tổng cộng: <span className="font-bold text-slate-200">{total}</span> mốc cước chi tiết
        </div>
        <div className="flex items-center gap-3">
          <span>
            Trang <span className="font-bold text-slate-200">{page}</span> / {totalPages}
          </span>
          <div className="flex items-center gap-1">
            <button
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page === 1}
              className="p-1 rounded border border-slate-800 bg-slate-900/40 text-slate-300 hover:bg-slate-800 disabled:opacity-40 disabled:hover:bg-transparent transition-colors"
            >
              <ChevronLeft size={14} />
            </button>
            <button
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page === totalPages}
              className="p-1 rounded border border-slate-800 bg-slate-900/40 text-slate-300 hover:bg-slate-800 disabled:opacity-40 disabled:hover:bg-transparent transition-colors"
            >
              <ChevronRight size={14} />
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
