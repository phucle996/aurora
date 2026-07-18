import { useState, useEffect, useCallback } from "react";
import { Search, Layers, ChevronLeft, ChevronRight, Pencil, Plus, Trash2, X } from "lucide-react";
import { billingApi, type TierDetail, type TierItem, type TierRangeInput } from "../../../lib/api/billing";
import { cn } from "../../../lib/utils";

// [COMMENT]: Định dạng phân tách dấu phẩy cho số nguyên để hiển thị MB trực quan (VD: 51200 -> 51,200)
function formatNumber(num: number): string {
  return num.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

function defaultEffectiveTime(latestEffectiveFrom?: string): string {
  const minimum = Date.now() + 60_000;
  const latest = latestEffectiveFrom ? new Date(latestEffectiveFrom).getTime() + 60_000 : minimum;
  const date = new Date(Math.max(minimum, latest));
  date.setMinutes(date.getMinutes() - date.getTimezoneOffset());
  return date.toISOString().slice(0, 16);
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
  const [editing, setEditing] = useState<TierDetail | null>(null);
  const [draftName, setDraftName] = useState("");
  const [draftRanges, setDraftRanges] = useState<TierRangeInput[]>([]);
  const [effectiveFrom, setEffectiveFrom] = useState(defaultEffectiveTime);
  const [changeReason, setChangeReason] = useState("");
  const [editLoading, setEditLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [editError, setEditError] = useState("");

  // [COMMENT]: Hàm fetch danh sách Tiers từ API backend
  const fetchTiers = useCallback(async () => {
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
  }, [page, serviceType, searchTerm]);

  // Tải lại dữ liệu khi đổi trang hoặc thay đổi bộ lọc
  useEffect(() => {
    fetchTiers();
  }, [fetchTiers]);

  // Reset về trang 1 khi đổi bộ lọc
  const handleFilterChange = (type: string) => {
    setServiceType(type);
    setPage(1);
  };

  const handleSearchChange = (term: string) => {
    setSearchTerm(term);
    setPage(1);
  };

  // [COMMENT]: Edit luôn fetch aggregate đầy đủ để pagination không làm mất child ranges khi publish.
  const openEdit = async (tier: TierItem) => {
    setEditLoading(true);
    setEditError("");
    try {
      const detail = await billingApi.getTierDetail(tier.code, tier.service_type);
      setEditing(detail);
      setDraftName(detail.name);
      setDraftRanges(detail.latest_version.ranges.map((range) => ({ ...range })));
      setEffectiveFrom(defaultEffectiveTime(detail.latest_version.effective_from));
      setChangeReason("");
    } catch (error) {
      setEditError(error instanceof Error ? error.message : "Không thể tải Tier để chỉnh sửa");
    } finally {
      setEditLoading(false);
    }
  };

  const updateDraftRange = (index: number, field: keyof TierRangeInput, value: number) => {
    setDraftRanges((current) => current.map((range, rangeIndex) =>
      rangeIndex === index ? { ...range, [field]: value } : range
    ));
  };

  const saveEdit = async () => {
    if (!editing) return;
    setSaving(true);
    setEditError("");
    const normalizedRanges = draftRanges.map(({ range_start, range_end, base_unit_price }) => ({
      range_start, range_end, base_unit_price,
    }));
    const originalRanges = editing.latest_version.ranges.map(({ range_start, range_end, base_unit_price }) => ({
      range_start, range_end, base_unit_price,
    }));
    const pricingChanged = JSON.stringify(normalizedRanges) !== JSON.stringify(originalRanges);
    const metadataChanged = draftName.trim() !== editing.name;

    try {
      // [COMMENT]: Pricing và metadata là hai OCC stream độc lập; sửa name không tạo pricing version.
      if (pricingChanged) {
        if (!changeReason.trim()) throw new Error("Cần nhập lý do thay đổi giá");
        await billingApi.createTierVersion({
          code: editing.code,
          service_type: editing.service_type,
          expected_latest_version: editing.latest_version.version_number,
          effective_from: new Date(effectiveFrom).toISOString(),
          change_reason: changeReason.trim(),
          ranges: normalizedRanges,
        });
      }
      if (metadataChanged) {
        await billingApi.updateTierMetadata({
          code: editing.code,
          service_type: editing.service_type,
          metadata_version: editing.metadata_version,
          name: draftName.trim(),
        });
      }
      setEditing(null);
      await fetchTiers();
    } catch (error) {
      setEditError(error instanceof Error ? error.message : "Không thể lưu thay đổi Tier");
    } finally {
      setSaving(false);
    }
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
              <th className="py-3 px-4 text-right">Thao tác</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-800/60 text-xs">
            {loading ? (
              <tr>
                <td colSpan={6} className="py-8 text-center text-slate-500">
                  Đang tải danh sách biểu giá...
                </td>
              </tr>
            ) : tiers.length === 0 ? (
              <tr>
                <td colSpan={6} className="py-8 text-center text-slate-500">
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
                    <td className="py-3.5 px-4 text-right">
                      <button
                        type="button"
                        onClick={() => openEdit(tier)}
                        disabled={editLoading}
                        className="inline-flex items-center gap-1 rounded border border-slate-700 px-2 py-1 text-[10px] font-semibold text-slate-300 hover:border-emerald-600 hover:text-emerald-400 disabled:opacity-50"
                      >
                        <Pencil size={11} /> Edit
                      </button>
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

      {editError && !editing && (
        <div className="rounded border border-red-500/30 bg-red-950/20 px-3 py-2 text-xs text-red-300">{editError}</div>
      )}

      {/* [COMMENT]: Modal publish full immutable snapshot; code/service type chỉ hiển thị read-only. */}
      {editing && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
          <div className="max-h-[90vh] w-full max-w-3xl overflow-y-auto rounded-lg border border-slate-700 bg-[#0b111a] shadow-2xl">
            <div className="flex items-center justify-between border-b border-slate-800 px-5 py-4">
              <div>
                <h3 className="text-sm font-bold text-slate-100">Chỉnh sửa Tier</h3>
                <p className="mt-1 font-mono text-[10px] text-slate-500">{editing.code} · {editing.service_type} · pricing v{editing.latest_version.version_number}</p>
              </div>
              <button type="button" onClick={() => setEditing(null)} className="text-slate-500 hover:text-slate-200"><X size={18} /></button>
            </div>

            <div className="space-y-5 p-5">
              <label className="block text-xs text-slate-400">
                Tên hiển thị
                <input value={draftName} onChange={(event) => setDraftName(event.target.value)}
                  className="mt-1.5 w-full rounded border border-slate-700 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-emerald-600" />
              </label>

              <div>
                <div className="mb-2 flex items-center justify-between">
                  <span className="text-xs font-semibold text-slate-300">Pricing ranges `[start,end)`</span>
                  <button type="button" onClick={() => setDraftRanges((ranges) => [...ranges, { range_start: 0, range_end: 0, base_unit_price: 0 }])}
                    className="inline-flex items-center gap-1 text-[10px] font-semibold text-emerald-400"><Plus size={12} /> Thêm range</button>
                </div>
                <div className="space-y-2">
                  {draftRanges.map((range, index) => (
                    <div key={range.id || `new-${index}`} className="grid grid-cols-[1fr_1fr_1fr_auto] gap-2 rounded border border-slate-800 bg-slate-950/60 p-2">
                      <input type="number" min={0} value={range.range_start} aria-label={`Range ${index + 1} start`}
                        onChange={(event) => updateDraftRange(index, "range_start", Number(event.target.value))}
                        className="rounded border border-slate-700 bg-slate-950 px-2 py-1.5 font-mono text-xs text-slate-200" />
                      <input type="number" min={0} value={range.range_end} aria-label={`Range ${index + 1} end`}
                        onChange={(event) => updateDraftRange(index, "range_end", Number(event.target.value))}
                        className="rounded border border-slate-700 bg-slate-950 px-2 py-1.5 font-mono text-xs text-slate-200" />
                      <input type="number" min={0} value={range.base_unit_price} aria-label={`Range ${index + 1} price`}
                        onChange={(event) => updateDraftRange(index, "base_unit_price", Number(event.target.value))}
                        className="rounded border border-slate-700 bg-slate-950 px-2 py-1.5 font-mono text-xs text-slate-200" />
                      <button type="button" aria-label={`Xóa range ${index + 1}`}
                        onClick={() => setDraftRanges((ranges) => ranges.filter((_, rangeIndex) => rangeIndex !== index))}
                        className="p-1.5 text-slate-600 hover:text-red-400"><Trash2 size={14} /></button>
                    </div>
                  ))}
                </div>
              </div>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <label className="text-xs text-slate-400">Có hiệu lực từ
                  <input type="datetime-local" value={effectiveFrom} onChange={(event) => setEffectiveFrom(event.target.value)}
                    className="mt-1.5 w-full rounded border border-slate-700 bg-slate-950 px-3 py-2 text-slate-200" />
                </label>
                <label className="text-xs text-slate-400">Lý do thay đổi giá
                  <input value={changeReason} onChange={(event) => setChangeReason(event.target.value)} placeholder="Bắt buộc khi ranges thay đổi"
                    className="mt-1.5 w-full rounded border border-slate-700 bg-slate-950 px-3 py-2 text-slate-200" />
                </label>
              </div>

              {editError && <div className="rounded border border-red-500/30 bg-red-950/20 px-3 py-2 text-xs text-red-300">{editError}</div>}
            </div>

            <div className="flex justify-end gap-2 border-t border-slate-800 px-5 py-4">
              <button type="button" onClick={() => setEditing(null)} className="rounded border border-slate-700 px-3 py-2 text-xs text-slate-300">Hủy</button>
              <button type="button" onClick={saveEdit} disabled={saving || draftRanges.length === 0 || !draftName.trim()}
                className="rounded bg-emerald-600 px-4 py-2 text-xs font-bold text-white hover:bg-emerald-500 disabled:opacity-50">
                {saving ? "Đang lưu..." : "Lưu thay đổi"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
