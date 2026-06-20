import { useMemo, useState, type ReactNode } from 'react'
import { Activity, CalendarDays, ChevronDown, Users } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { usePageMeta } from '@/lib/page-meta'
import { cn } from '@/lib/utils'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { format, subDays } from 'date-fns'
import { type DateRange } from 'react-day-picker'
import { useZoneStore } from '@/hooks/useZoneStore'

// Nhập các tab nghiệp vụ chi tiết đã được module hóa
import { OverviewTab } from './tabs/OverviewTab'
import { ConsumersTab } from './tabs/ConsumersTab'

// Khai báo các loại tab được hỗ trợ trong Mail Page (sau khi đã loại bỏ Gateways và Endpoints)
type TabKey = 'overview' | 'consumers'

// Cấu hình các Tabs hiển thị ở menu điều hướng bao gồm icon và mô tả
const tabs: Array<{ key: TabKey; label: string; description: string; icon: ReactNode }> = [
  { key: 'overview', label: 'Overview', description: 'Mail overview', icon: <Activity className="size-4" /> },
  { key: 'consumers', label: 'Consumers', description: 'Mail consumers & usage', icon: <Users className="size-4" /> },
]

/**
 * Component trang chính quản trị Mail Admin (MailPage)
 * Đóng vai trò là entrypoint chính điều phối trạng thái, phân tách tab, các bộ lọc toàn cục (Khoảng thời gian và Zone từ Store).
 */
export default function MailPage() {
  // Cập nhật metadata động cho tài liệu HTML của trang Mail Admin
  usePageMeta('Mail Admin | Aurora Admin', 'Monitor Mail traffic, endpoints, gateways, and runtime aggregation.')
  
  // Xác định tab đang hoạt động hiện tại (đọc từ Hash URL nếu có để phục vụ deeplinking tiện lợi)
  const [active, setActive] = useState<TabKey>(() => {
    const hash = window.location.hash
    if (hash === '#consumers') return 'consumers'
    return 'overview'
  })

  // Đọc Zone ID đang hoạt động từ RAM Cache (Global Zustand Store)
  const { activeZone } = useZoneStore()

  // Điều kiện ẩn hiện bộ lọc khoảng thời gian (Date Range)
  const showsDateRangeFilter = active === 'overview' || active === 'consumers'

  // State cấu hình khoảng thời gian mặc định (7 ngày gần nhất)
  const [dateRange, setDateRange] = useState<DateRange | undefined>({
    from: subDays(new Date(), 7),
    to: new Date(),
  })

  // Chuỗi nhãn hiển thị khoảng thời gian đã chọn (Ví dụ: "May 20 – May 27, 2026")
  const dateRangeLabel = useMemo(() => {
    if (!dateRange?.from) return 'Select range'
    if (!dateRange.to) return format(dateRange.from, 'LLL d, y')
    return `${format(dateRange.from, 'LLL d')} – ${format(dateRange.to, 'LLL d, y')}`
  }, [dateRange])

  return (
    <div className="min-w-0 space-y-6 pb-8">
      {/* Header chính tích hợp Bộ chọn Tab và Bộ lọc khoảng thời gian theo phong cách Segmented Control */}
      <div className="flex flex-col gap-6 md:flex-row md:items-center md:justify-between border-b border-border/40 pb-5">
        <div className="space-y-1.5">
          <h1 className="text-2xl font-bold tracking-tight text-foreground sm:text-3xl">Mail Admin</h1>
          <p className="text-xs text-muted-foreground max-w-2xl">
            Platform-wide visibility for Mail delivery, routing, and infrastructure health across all organizations.
          </p>
        </div>
        
        {/* Nhóm điều khiển nằm bên phải trên desktop */}
        <div className="flex flex-wrap items-center gap-3 self-start md:self-auto">
          {/* Menu Tab được thiết kế dưới dạng Segmented Control siêu gọn nhẹ */}
          <div className="inline-flex items-center p-0.5 bg-muted/40 border border-border/60 rounded-lg backdrop-blur-md shadow-sm">
            {tabs.map((tab) => {
              const isActive = active === tab.key
              return (
                <button
                  key={tab.key}
                  type="button"
                  onClick={() => {
                    setActive(tab.key)
                    window.location.hash = tab.key
                  }}
                  className={cn(
                    'relative flex items-center gap-2 px-3 py-1.5 text-xs font-semibold rounded-md transition-all duration-200 cursor-pointer select-none',
                    isActive
                      ? 'bg-background text-foreground shadow-sm border border-border/40 font-semibold'
                      : 'text-muted-foreground hover:text-foreground hover:bg-muted-foreground/5',
                  )}
                >
                  {tab.icon}
                  <span>{tab.label}</span>
                </button>
              )
            })}
          </div>

          {/* Bộ chọn khoảng thời gian (chỉ hiện khi tab hiện tại yêu cầu) */}
          {showsDateRangeFilter ? (
            <Popover>
              <PopoverTrigger asChild>
                <Button variant="outline" className="h-8 min-w-40 justify-between gap-2 border-border/80 bg-card/60 px-3 text-xs shadow-sm cursor-pointer hover:bg-accent hover:text-accent-foreground">
                  <span className="flex items-center gap-1.5">
                    <CalendarDays className="size-3.5 text-muted-foreground" />
                    {dateRangeLabel}
                  </span>
                  <ChevronDown className="size-3.5 text-muted-foreground" />
                </Button>
              </PopoverTrigger>
              <PopoverContent className="w-auto p-0" align="end">
                <Calendar
                  initialFocus
                  mode="range"
                  defaultMonth={dateRange?.from}
                  selected={dateRange}
                  onSelect={setDateRange}
                  numberOfMonths={2}
                />
              </PopoverContent>
            </Popover>
          ) : null}
        </div>
      </div>

      {/* Vùng hiển thị nội dung chi tiết của Tab đang kích hoạt */}
      <div className="transition-all duration-300 ease-in-out">
        {active === 'overview' && <OverviewTab zoneID={activeZone} dateRange={dateRange} />}
        {active === 'consumers' && <ConsumersTab zoneID={activeZone} dateRange={dateRange} />}
      </div>
    </div>
  )
}
