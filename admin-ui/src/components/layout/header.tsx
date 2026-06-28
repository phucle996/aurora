/**
 * @file header.tsx
 * @description Component AppHeader cho giao diện quản trị (Admin UI).
 * Cung cấp thanh điều hướng trên cùng bao gồm:
 * - Điều khiển thu gọn/mở rộng thanh bên (Sidebar) trên Desktop và Mobile.
 * - Ô tìm kiếm nhanh toàn cục (metrics, tenants, dashboards).
 * - Bộ chọn Zone (Vùng dữ liệu) hỗ trợ kiến trúc Multi-Zone / Cloud Native.
 * - Nút thông báo (Bell Icon), Trợ giúp (Help Icon).
 * - Bộ chuyển đổi giao diện Sáng/Tối (ThemeSwitcher).
 * - Menu tài khoản quản trị viên và chức năng Đăng xuất an toàn.
 * 
 * @security
 * - Xử lý đăng xuất đảm bảo giải phóng token/cookie ở cả phía Client và API Gateway/Backend.
 * - CSRF & XSSI Protection: Tương tác với API qua client `Fetch` đã được cấu hình bảo mật.
 * 
 * @performance
 * - Tối ưu hóa tính toán nhãn hiển thị của Zone bằng `useMemo` để tránh re-render không cần thiết.
 * - Sử dụng dependency array chuẩn xác trong `useEffect` để tránh vòng lặp fetch dữ liệu vô tận.
 */

import {
  Bell,
  ChevronDown,
  CircleHelp,
  Globe2,
  LogOut,
  Menu,
  PanelLeft,
  Search,
} from 'lucide-react'

import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import ThemeSwitcher from '@/components/layout/theme-switcher'
import { useAdminSession } from '@/hooks/useAdminSession'
import { useZoneStore } from '@/hooks/useZoneStore'
import { useEffect, useMemo } from 'react'
import { Fetch } from '@/lib/fetch'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'

/**
 * Định nghĩa Props cho AppHeader
 * @param onToggleSidebar Callback kích hoạt ẩn/hiện sidebar trên màn hình Desktop (lg trở lên).
 * @param onOpenMobileSidebar Callback mở sidebar dạng Drawer/Overlay trên màn hình Mobile.
 */
type AppHeaderProps = {
  onToggleSidebar: () => void
  onOpenMobileSidebar: () => void
}

export default function AppHeader({ onToggleSidebar, onOpenMobileSidebar }: AppHeaderProps) {
  // Hook quản lý session của Admin (lưu trữ token, thông tin user và cơ chế xóa session).
  const { clearSession } = useAdminSession()
  
  // Hook kết nối tới global store quản lý danh sách Zone và Zone hiện tại đang active.
  // Trong môi trường Cloud Native & High Availability (HA), hệ thống được chia làm nhiều Zone vật lý/logic 
  // để tăng tính cô lập lỗi (fault isolation) và giảm độ trễ (latency).
  const { zones, activeZone, fetchZones, setActiveZone } = useZoneStore()

  /**
   * Tự động lấy danh sách Zone từ API Gateway/Backend khi component được mount.
   * Danh sách Zone này quyết định các lựa chọn trong dropdown.
   * `void` được sử dụng để báo hiệu rằng promise được trả về cố ý không cần await trực tiếp (non-blocking).
   */
  useEffect(() => {
    void fetchZones()
  }, [fetchZones])

  /**
   * Tính toán nhãn hiển thị cho Zone đang được chọn.
   * Sử dụng `useMemo` để lưu lại giá trị (memoize), chỉ tính toán lại khi `activeZone` hoặc mảng `zones` thay đổi.
   * Nếu `activeZone` là null/undefined, mặc định context hoạt động ở chế độ 'Global' (xem dữ liệu của tất cả các vùng).
   */
  const activeZoneLabel = useMemo(() => {
    if (!activeZone) return 'Global'
    return zones.find(z => z.code === activeZone)?.name || activeZone
  }, [activeZone, zones])

  /**
   * Xử lý đăng xuất an toàn (Secure Logout Flow).
   * 
   * @flow
   * 1. Gửi request POST tới `/admin/auth/logout` để thông báo cho backend/API Gateway thu hồi (revoke) 
   *    session cookie hoặc JWT token nhằm chống tấn công Session Hijacking sau khi thoát.
   * 2. Bất kể request mạng thành công hay thất bại (như mất mạng, API Gateway gặp sự cố - HA Node die), 
   *    khối `finally` vẫn đảm bảo thực thi `clearSession()` để xóa sạch tokens/state trên Client (IndexedDB/LocalStorage),
   *    tránh tình trạng người dùng bị kẹt không thể đăng xuất cục bộ.
   */
  /**
   * Xử lý chuyển đổi Zone an toàn (Zone-Aware Switch Flow).
   * Gửi request POST tới `/admin/zone/go-to-zone?zone_code=...` để biên thực hiện chuyển vùng,
   * ký lại bộ token mới và ghi nhận cookie/phân vùng mới cho Admin.
   */
  const handleZoneChange = async (zoneCode: string | null) => {
    if (zoneCode === activeZone) return
    try {
      const target = zoneCode || 'global'
      const resp = await Fetch(`/admin/zone/go-to-zone?zone_code=${encodeURIComponent(target)}`, { method: 'POST' })
      if (!resp.ok) {
        throw new Error('Failed to refresh session for the selected zone')
      }
      setActiveZone(zoneCode)
    } catch (err) {
      console.error('Failed to switch zone:', err)
    }
  }

  const handleLogout = async () => {
    try {
      await Fetch('/admin/auth/logout', { method: 'POST' })
    } catch (err) {
      console.error('Failed to log out on server:', err)
    } finally {
      clearSession()
    }
  }

  return (
    // Sử dụng sticky header để cố định thanh điều hướng ở trên cùng khi cuộn trang, giúp admin truy cập nhanh các chức năng.
    <header className="sticky top-0 z-20 border-b border-border/70 bg-card shadow-[0_1px_0_rgba(15,23,42,0.03)]">
      <div className="flex min-h-14 flex-wrap items-center gap-3 px-4 py-3 md:px-6 md:py-0">
        
        {/* Nút Trigger Sidebar dành cho Mobile (Hiện ở màn hình < lg) */}
        <button
          type="button"
          onClick={onOpenMobileSidebar}
          className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground lg:hidden"
          aria-label="Open navigation"
        >
          <Menu className="h-4 w-4" />
        </button>

        {/* Nút Toggle Sidebar dành cho Desktop (Hiện ở màn hình >= lg) */}
        <button
          type="button"
          onClick={onToggleSidebar}
          className="hidden h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground lg:inline-flex"
          aria-label="Collapse sidebar"
        >
          <PanelLeft className="h-4 w-4" />
        </button>

        {/* Ô Tìm Kiếm Toàn Cục (Ẩn ở màn hình cực nhỏ < sm) */}
        <div className="order-3 relative hidden sm:block md:order-2 w-64">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder="Search dashboard, metrics, tenants..."
            className="h-9 w-full border-border/80 bg-background pl-9 text-sm shadow-none"
          />
        </div>

        {/* 
          Bộ chọn Zone Toàn cục (Global Zone Selector Dropdown)
          Cho phép quản trị viên chuyển đổi nhanh context hiển thị dữ liệu giữa các vùng độc lập (US-East, VN-West, v.v.).
          Việc chọn một Zone sẽ trigger cập nhật trạng thái trong `useZoneStore` và đồng bộ hóa các API request
          khác trên UI để truy vấn dữ liệu từ đúng cluster/vùng đó.
        */}
        <div className="order-3 md:order-2 w-64">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                variant="outline"
                className="h-9 w-full justify-between gap-2 border-border/80 bg-background px-3 text-xs shadow-none cursor-pointer hover:bg-accent"
              >
                <span className="flex items-center gap-1.5 font-medium truncate">
                  <Globe2 className="size-3.5 text-muted-foreground shrink-0" />
                  <span className="truncate">{activeZoneLabel}</span>
                </span>
                <ChevronDown className="size-3.5 text-muted-foreground shrink-0" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-64">
              {/* Option Global: Không giới hạn vùng hiển thị (hiển thị tổng hợp) */}
              <DropdownMenuItem onClick={() => handleZoneChange(null)} className="cursor-pointer">
                Global (All Zones)
              </DropdownMenuItem>
              {/* Render danh sách các Zone lấy về từ API */}
              {zones.map((zone) => (
                <DropdownMenuItem
                  key={zone.id}
                  onClick={() => handleZoneChange(zone.code)}
                  className="cursor-pointer"
                >
                  {zone.name}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        {/* Khối chức năng bên phải Header (Thông báo, Trợ giúp, Theme, Profile) */}
        <div className="order-2 ml-auto flex items-center gap-1 md:order-3 md:gap-2">
          
          {/* Nút Thông báo (Notifications) với chấm đỏ primary báo hiệu tin nhắn mới */}
          <button
            type="button"
            className="relative inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            aria-label="Notifications"
          >
            <Bell className="h-4 w-4" />
            <span className="absolute right-1 top-1 h-2.5 w-2.5 rounded-full bg-primary" />
          </button>

          {/* Nút Trợ giúp (Help Docs) */}
          <button
            type="button"
            className="hidden h-8 w-8 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground sm:inline-flex"
            aria-label="Help"
          >
            <CircleHelp className="h-4 w-4" />
          </button>

          {/* Component chuyển đổi Theme (Sáng/Tối/Hệ thống) */}
          <ThemeSwitcher />

          {/* Dropdown Menu tài khoản Admin */}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                className="inline-flex items-center gap-2 rounded-md px-1 py-1 transition-colors hover:bg-accent sm:px-2 sm:py-1.5 focus:outline-none cursor-pointer"
              >
                {/* Avatar đại diện với Fallback kí tự "SA" (System Admin) */}
                <Avatar className="h-8 w-8 border border-border/70">
                  <AvatarFallback className="bg-primary text-xs font-bold text-primary-foreground">SA</AvatarFallback>
                </Avatar>
                {/* Thông tin tên và chức danh (Ẩn ở màn hình nhỏ, chỉ hiện từ lg trở lên) */}
                <div className="hidden min-w-0 text-left lg:block">
                  <p className="truncate text-xs font-bold text-foreground">System Admin</p>
                  <p className="truncate text-[11px] text-muted-foreground">Platform Administrator</p>
                </div>
                <ChevronDown className="hidden h-4 w-4 text-muted-foreground lg:block" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-40">
              {/* Nút Đăng xuất với kiểu dáng destructive (màu đỏ) cảnh báo hành động nhạy cảm */}
              <DropdownMenuItem onClick={handleLogout} variant="destructive" className="cursor-pointer flex items-center gap-2">
                <LogOut className="h-4 w-4" />
                <span>Đăng xuất</span>
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
    </header>
  )
}

