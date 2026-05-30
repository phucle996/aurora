import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Activity, CalendarDays, ChevronDown, Globe2, Link2, Server, Users } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Fetch } from '@/lib/fetch'
import { usePageMeta } from '@/lib/page-meta'
import { cn } from '@/lib/utils'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { format, subDays } from 'date-fns'
import { type DateRange } from 'react-day-picker'

// Nhập các tab nghiệp vụ chi tiết đã được module hóa
import { OverviewTab } from './tabs/OverviewTab'
import { ConsumersTab } from './tabs/ConsumersTab'
import { GatewaysTab } from './tabs/GatewaysTab'
import { EndpointsTab } from './tabs/EndpointsTab'

// Khai báo các loại tab được hỗ trợ trong Mail Page
type TabKey = 'overview' | 'consumers' | 'gateways' | 'endpoints'

// Cấu hình các Tabs hiển thị ở menu điều hướng bao gồm icon và mô tả
const tabs: Array<{ key: TabKey; label: string; description: string; icon: ReactNode }> = [
  { key: 'overview', label: 'Overview', description: 'Mail overview', icon: <Activity className="size-4" /> },
  { key: 'consumers', label: 'Consumers', description: 'Mail consumers & usage', icon: <Users className="size-4" /> },
  { key: 'gateways', label: 'Gateways', description: 'Mail gateways & routing', icon: <Server className="size-4" /> },
  { key: 'endpoints', label: 'Endpoints', description: 'Mail endpoints & targets', icon: <Link2 className="size-4" /> },
]

/**
 * Component trang chính quản trị Mail Admin (MailPage)
 * Đóng vai trò là entrypoint chính điều phối trạng thái, phân tách tab, các bộ lọc toàn cục (Zone và Khoảng thời gian).
 */
export default function MailPage() {
  // Cập nhật metadata động cho tài liệu HTML của trang Mail Admin
  usePageMeta('Mail Admin | Aurora Admin', 'Monitor Mail traffic, endpoints, gateways, and runtime aggregation.')
  
  // Xác định tab đang hoạt động hiện tại (đọc từ Hash URL nếu có để phục vụ deeplinking tiện lợi)
  const [active, setActive] = useState<TabKey>(() => {
    const hash = window.location.hash
    if (hash === '#endpoints') return 'endpoints'
    if (hash === '#consumers') return 'consumers'
    if (hash === '#gateways') return 'gateways'
    return 'overview'
  })

  // State quản lý danh sách các vùng (Zones) và vùng đang được chọn để lọc
  const [zones, setZones] = useState<Array<{ id: string; name: string }>>([])
  const [selectedZone, setSelectedZone] = useState<string | null>(null)

  // Điều kiện ẩn hiện bộ lọc Zone dựa trên Tab đang chọn (ví dụ: endpoints không cần lọc zone)
  const showsZoneFilter = active !== 'endpoints'
  // Điều kiện ẩn hiện bộ lọc khoảng thời gian (Date Range)
  const showsDateRangeFilter = active === 'overview' || active === 'consumers'

  // Gọi API tải danh sách Vùng hạ tầng khi bộ lọc Zone được phép hiển thị
  useEffect(() => {
    if (!showsZoneFilter || zones.length > 0) {
      return
    }
    async function loadZones() {
      try {
        const resp = await Fetch('/admin/zones')
        if (resp.ok) {
          const body = await resp.json()
          setZones(body.data?.items || [])
        }
      } catch (err) {
        console.error('Failed to load zones', err)
      }
    }
    void loadZones()
  }, [showsZoneFilter, zones.length])

  // State cấu hình khoảng thời gian mặc định (7 ngày gần nhất)
  const [dateRange, setDateRange] = useState<DateRange | undefined>({
    from: subDays(new Date(), 7),
    to: new Date(),
  })

  // Tên hiển thị vùng đang chọn trên nút Dropdown
  const activeZoneLabel = useMemo(() => {
    if (!selectedZone) return 'All Zones'
    return zones.find(z => z.id === selectedZone)?.name || selectedZone
  }, [selectedZone, zones])

  // Chuỗi nhãn hiển thị khoảng thời gian đã chọn (Ví dụ: "May 20 – May 27, 2026")
  const dateRangeLabel = useMemo(() => {
    if (!dateRange?.from) return 'Select range'
    if (!dateRange.to) return format(dateRange.from, 'LLL d, y')
    return `${format(dateRange.from, 'LLL d')} – ${format(dateRange.to, 'LLL d, y')}`
  }, [dateRange])

  return (
    <div className="min-w-0 space-y-4 pb-8">
      {/* Header chính và Bộ lọc */}
      <div className="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
        <div className="space-y-1">
          <h1 className="aurora-page-title text-foreground">Mail Admin</h1>
          <p className="aurora-page-subtitle">
            Platform-wide visibility for Mail delivery, routing, and infrastructure health across all organizations.
          </p>
        </div>
        
        {/* Bộ lọc Vùng và Thời gian hiển thị ở góc bên phải */}
        {showsZoneFilter || showsDateRangeFilter ? (
          <div className="flex flex-wrap items-center gap-3">
            {/* Bộ lọc Vùng (Zone Dropdown) */}
            {showsZoneFilter ? (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline" className="h-10 min-w-36 justify-between gap-3 border-border/80 bg-card px-4 aurora-filter-text shadow-sm cursor-pointer">
                    <span className="flex items-center gap-2">
                      <Globe2 className="size-4" />
                      {activeZoneLabel}
                    </span>
                    <ChevronDown className="size-4 text-muted-foreground" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-56">
                  <DropdownMenuItem onClick={() => setSelectedZone(null)} className="cursor-pointer">
                    All Zones
                  </DropdownMenuItem>
                  {zones.map((zone) => (
                    <DropdownMenuItem key={zone.id} onClick={() => setSelectedZone(zone.id)} className="cursor-pointer">
                      {zone.name}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            ) : null}

            {/* Bộ lọc Khoảng thời gian (Date Range Calendar Popover) */}
            {showsDateRangeFilter ? (
              <Popover>
                <PopoverTrigger asChild>
                  <Button variant="outline" className="h-10 min-w-44 justify-between gap-3 border-border/80 bg-card px-4 aurora-filter-text shadow-sm cursor-pointer">
                    <span className="flex items-center gap-2">
                      <CalendarDays className="size-4" />
                      {dateRangeLabel}
                    </span>
                    <ChevronDown className="size-4 text-muted-foreground" />
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
        ) : null}
      </div>

      {/* Menu các Tab điều hướng */}
      <div className="rounded-xl border border-border bg-card p-1 shadow-sm">
        <div className="grid grid-cols-2 gap-1 lg:grid-cols-4">
          {tabs.map((tab) => (
            <button
              key={tab.key}
              type="button"
              onClick={() => {
                setActive(tab.key)
                window.location.hash = tab.key
              }}
              className={cn(
                'flex items-center gap-3 rounded-lg px-4 py-3 text-left transition-colors cursor-pointer',
                active === tab.key ? 'bg-primary/5 text-primary ring-1 ring-primary/15' : 'text-muted-foreground hover:bg-muted/70 hover:text-foreground',
              )}
            >
              {/* Vùng vẽ Icon tương ứng với Tab */}
              <span className={cn('inline-flex size-8 items-center justify-center rounded-full', active === tab.key ? 'bg-primary/10' : 'bg-muted')}>
                {tab.icon}
              </span>
              <span>
                <span className="aurora-tab-label">{tab.label}</span>
                <span className="aurora-tab-description">{tab.description}</span>
              </span>
            </button>
          ))}
        </div>
      </div>

      {/* Vùng hiển thị nội dung chi tiết của Tab đang kích hoạt */}
      <div className="transition-all duration-300 ease-in-out">
        {active === 'overview' && <OverviewTab zoneID={selectedZone} dateRange={dateRange} />}
        {active === 'consumers' && <ConsumersTab zoneID={selectedZone} dateRange={dateRange} />}
        {active === 'gateways' && <GatewaysTab zoneID={selectedZone} />}
        {active === 'endpoints' && <EndpointsTab zoneID={selectedZone} />}
      </div>
    </div>
  )
}
