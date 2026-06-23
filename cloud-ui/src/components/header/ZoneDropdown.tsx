"use client";
import React, { useState, useEffect } from "react";
import { Dropdown } from "../ui/dropdown/Dropdown";
import { DropdownItem } from "../ui/dropdown/DropdownItem";
import { fetchZoneCatalog, switchZone, type ZoneCatalogItem } from "@/lib/api/zone";

// [COMMENT]: Hàm helper Client-side dùng để trích xuất giá trị cookie theo tên từ document.cookie.
// Không dùng HttpOnly cookie ở đây để JS có thể đọc được cấu trúc hiển thị zone hiện tại.
function getCookie(name: string): string | null {
  if (typeof document === "undefined") return null;
  const value = `; ${document.cookie}`;
  const parts = value.split(`; ${name}=`);
  if (parts.length === 2) return parts.pop()?.split(";").shift() || null;
  return null;
}

export default function ZoneDropdown() {
  const [isOpen, setIsOpen] = useState(false);
  const [zones, setZones] = useState<ZoneCatalogItem[]>([]);
  const [activeZoneCode, setActiveZoneCode] = useState<string>("");
  const [isLoading, setIsLoading] = useState<boolean>(true);

  // [COMMENT]: Lấy danh sách Zone từ Ingress Gateway và kiểm tra active zone hiện tại trong Cookie.
  useEffect(() => {
    async function initZoneContext() {
      try {
        // [COMMENT]: 1. Đọc zone_code hiện thời lưu ở Cookie của trình duyệt.
        const cachedCode = getCookie("zone_code");
        if (cachedCode) {
          setActiveZoneCode(cachedCode.toUpperCase());
        }

        // [COMMENT]: 2. Gọi Edge Catalog API để tải danh sách các Zone đang hoạt động.
        const list = await fetchZoneCatalog();
        setZones(list);

        // [COMMENT]: 3. Nếu cookie chưa có nhưng có danh sách zone, fallback chọn zone đầu tiên.
        if (!cachedCode && list.length > 0) {
          setActiveZoneCode(list[0].code.toUpperCase());
        }
      } catch (error) {
        console.error("Lỗi khởi tạo danh mục Zone:", error);
      } finally {
        setIsLoading(false);
      }
    }
    initZoneContext();
  }, []);

  // [COMMENT]: Toggle hiển thị menu thả xuống.
  const toggleDropdown = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation();
    setIsOpen((prev) => !prev);
  };

  const closeDropdown = () => {
    setIsOpen(false);
  };

  // [COMMENT]: Xử lý đổi active zone khi người dùng click chọn một cụm zone khác.
  const handleZoneSwitch = async (zoneCode: string) => {
    closeDropdown();
    if (zoneCode.toUpperCase() === activeZoneCode) return;

    try {
      // [COMMENT]: Gọi API chuyển đổi zone tường minh của Rust ACL biên.
      const result = await switchZone(zoneCode.toLowerCase());

      // [COMMENT]: Cập nhật state cục bộ.
      setActiveZoneCode(result.zone_code.toUpperCase());

      // [COMMENT]: Thực hiện reload toàn trang (Full Page Reload). Đây là phương án an toàn nhất
      // để giải phóng hoàn toàn cache (React Query, SWR, Global State) của zone cũ, ngăn ngừa
      // việc rò rỉ dữ liệu hoặc hiển thị sai lệch data giữa các zone.
      window.location.reload();
    } catch (error) {
      console.error("Chuyển đổi zone thất bại:", error);
      alert("Không thể chuyển vùng dữ liệu. Vui lòng thử lại!");
    }
  };

  // [COMMENT]: Render giao diện dropdown chọn vùng dữ liệu với icon Globe cao cấp.
  return (
    <div className="relative">
      <button
        onClick={toggleDropdown}
        disabled={isLoading || zones.length === 0}
        className="dropdown-toggle flex items-center justify-center gap-1.5 rounded-lg border border-gray-200 bg-transparent px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-800 dark:text-gray-400 dark:hover:bg-white/5 h-11"
        title="Chọn Vùng Dữ Liệu (Active Zone)"
      >
        <span className="text-base" role="img" aria-label="globe">
          🌐
        </span>
        <span className="hidden sm:inline font-semibold">
          {isLoading ? "Đang tải..." : activeZoneCode || "Chọn Zone"}
        </span>
        <svg
          className={`stroke-gray-500 dark:stroke-gray-400 transition-transform duration-200 ${isOpen ? "rotate-180" : ""
            }`}
          width="16"
          height="16"
          viewBox="0 0 18 20"
          fill="none"
          xmlns="http://www.w3.org/2000/svg"
        >
          <path
            d="M4.3125 8.65625L9 13.3437L13.6875 8.65625"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </button>

      <Dropdown
        isOpen={isOpen}
        onClose={closeDropdown}
        className="absolute right-0 mt-2 flex w-[200px] flex-col rounded-xl border border-gray-200 bg-white p-2 shadow-theme-lg dark:border-gray-800 dark:bg-gray-dark z-9999"
      >
        <div className="px-3 py-1.5 border-b border-gray-100 dark:border-gray-800 mb-1">
          <span className="block text-xs font-semibold text-gray-400 uppercase tracking-wider">
            Vùng Dữ Liệu
          </span>
        </div>
        <ul className="flex flex-col gap-0.5 max-h-[240px] overflow-y-auto">
          {zones.map((z) => {
            const isSelected = z.code.toUpperCase() === activeZoneCode;
            return (
              <li key={z.id}>
                <DropdownItem
                  onItemClick={() => handleZoneSwitch(z.code)}
                  className={`flex items-center justify-between w-full px-3 py-2 text-theme-sm font-medium rounded-lg text-left transition-colors duration-150 ${isSelected
                      ? "bg-brand-50 text-brand-600 dark:bg-brand-950/30 dark:text-brand-400"
                      : "text-gray-700 hover:bg-gray-50 dark:text-gray-400 dark:hover:bg-white/5"
                    }`}
                >
                  <div className="flex items-center gap-2">
                    <span className="text-xs">🌍</span>
                    <span>{z.name}</span>
                  </div>
                  {isSelected && (
                    <span className="text-brand-500 dark:text-brand-400 text-xs">
                      ✓
                    </span>
                  )}
                </DropdownItem>
              </li>
            );
          })}
        </ul>
      </Dropdown>
    </div>
  );
}
