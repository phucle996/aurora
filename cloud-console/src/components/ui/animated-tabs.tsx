"use client";

import React, { useRef, useState, useEffect } from "react";
import { cn } from "@/lib/utils";

// [COMMENT]: Khai báo interface đại diện cho một phần tử Tab
export interface TabItem {
  id: string;      // ID định danh duy nhất cho mỗi tab (ví dụ: "organizations")
  label: string;   // Nhãn hiển thị bằng văn bản trên tab (ví dụ: "Organizations")
  count?: number;  // Số lượng đính kèm tùy chọn (ví dụ: số lượng thư mời chưa xử lý)
}

// [COMMENT]: Khai báo các thuộc tính (Props) cho component AnimatedTabs
export interface AnimatedTabsProps {
  tabs: TabItem[];                                      // Danh sách cấu hình các tabs
  activeTab: string;                                    // ID của tab hiện tại đang được chọn hoạt động
  onChange: (id: string) => void;                       // Hàm callback kích hoạt khi click đổi tab
  className?: string;                                   // Class CSS bổ sung cho thẻ bọc ngoài cùng (container)
  indicatorClassName?: string;                           // Class CSS tùy chỉnh cho thanh line trượt (mặc định là xanh lá/emerald)
  activeTextColorClassName?: string;                    // Class CSS tùy chỉnh cho màu chữ khi tab active (mặc định là emerald)
}

export function AnimatedTabs({
  tabs,
  activeTab,
  onChange,
  className,
  indicatorClassName = "bg-emerald-500 dark:bg-emerald-400",
  activeTextColorClassName = "text-emerald-600 dark:text-emerald-400",
}: AnimatedTabsProps) {
  // [COMMENT]: State quản lý tọa độ left (độ lệch ngang) và width (chiều rộng) của indicator trượt
  const [coords, setCoords] = useState<{ left: number; width: number }>({ left: 0, width: 0 });
  
  // [COMMENT]: Ref tham chiếu đến container chứa toàn bộ tab
  const containerRef = useRef<HTMLDivElement>(null);
  
  // [COMMENT]: Ref tham chiếu đến phần tử button của tab đang hoạt động hiện tại
  const activeTabRef = useRef<HTMLButtonElement>(null);

  // [COMMENT]: Effect xử lý cập nhật vị trí thanh trượt khi tab thay đổi hoặc khi resize giao diện
  useEffect(() => {
    // [COMMENT]: Hàm tính toán và cập nhật tọa độ cho thanh trượt
    const updateIndicator = () => {
      const activeElement = activeTabRef.current;
      const container = containerRef.current;
      
      // Nếu tồn tại cả phần tử tab active và container bọc ngoài
      if (activeElement && container) {
        const activeRect = activeElement.getBoundingClientRect();
        const containerRect = container.getBoundingClientRect();
        
        // Tính toán left tương đối so với container và bù trừ thêm giá trị scrollLeft nếu container bị cuộn ngang
        setCoords({
          left: activeRect.left - containerRect.left + container.scrollLeft,
          width: activeRect.width,
        });
      }
    };

    // Thực hiện cập nhật tọa độ tức thì khi render lần đầu hoặc đổi tab
    updateIndicator();

    // [COMMENT]: Đăng ký ResizeObserver để tự động cập nhật lại tọa độ thanh trượt khi layout thay đổi
    // (Ví dụ: kéo giãn cửa sổ trình duyệt, đóng/mở sidebar, hoặc font chữ thay đổi kích thước)
    if (typeof window !== "undefined" && containerRef.current) {
      const resizeObserver = new ResizeObserver(() => {
        updateIndicator();
      });
      resizeObserver.observe(containerRef.current);
      
      // Cleanup observer khi component unmount để tránh rò rỉ bộ nhớ (memory leaks)
      return () => {
        resizeObserver.disconnect();
      };
    }
  }, [activeTab, tabs]);

  return (
    <div
      ref={containerRef}
      className={cn(
        "relative flex border-b border-slate-200 dark:border-slate-800 overflow-x-auto no-scrollbar scroll-smooth",
        className
      )}
    >
      {/* [COMMENT]: Vòng lặp kết xuất danh sách các button Tab */}
      {tabs.map((tab) => {
        const isActive = tab.id === activeTab;
        return (
          <button
            key={tab.id}
            ref={isActive ? activeTabRef : null}
            onClick={() => onChange(tab.id)}
            className={cn(
              "pb-2 px-3 text-[13px] font-bold transition-colors duration-200 relative flex items-center gap-1.5 shrink-0 select-none cursor-pointer outline-none focus-visible:text-slate-800 dark:focus-visible:text-slate-100",
              isActive
                ? activeTextColorClassName
                : "text-slate-400 hover:text-slate-600 dark:hover:text-slate-200"
            )}
          >
            {/* Nhãn văn bản của Tab */}
            <span>{tab.label}</span>
            
            {/* [COMMENT]: Render badge hiển thị số lượng dạng góc bo 4px (rounded-[4px]) theo chuẩn thiết kế Enterprise mới */}
            {tab.count !== undefined && tab.count > 0 && (
              <span
                className={cn(
                  "flex h-4 min-w-4 items-center justify-center rounded-[4px] px-1 text-[11px] font-bold transition-colors duration-200",
                  isActive
                    ? "bg-emerald-100 text-emerald-600 dark:bg-emerald-950/40 dark:text-emerald-400"
                    : "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400"
                )}
              >
                {tab.count}
              </span>
            )}
          </button>
        );
      })}

      {/* [COMMENT]: Thẻ hiển thị thanh line trượt màu xanh lá chuyển động mượt mà */}
      <div
        className={cn(
          "absolute bottom-0 h-0.5 transition-all duration-300 ease-out pointer-events-none",
          indicatorClassName
        )}
        style={{
          left: `${coords.left}px`,
          width: `${coords.width}px`,
        }}
      />
    </div>
  );
}
