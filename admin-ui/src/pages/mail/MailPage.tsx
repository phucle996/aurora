import { useMemo, useState } from 'react'
import { CalendarDays, ChevronDown } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { usePageMeta } from '@/lib/page-meta'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { format, subDays } from 'date-fns'
import { type DateRange } from 'react-day-picker'
import { useZoneStore } from '@/hooks/useZoneStore'

import { ConsumersTab } from './tabs/ConsumersTab'

/**
 * Component trang chính quản trị Mail Admin (MailPage)
 * Trang Admin chỉ giữ consumer aggregation. Hạ tầng vật lý được quan sát qua Grafana/OTel.
 */
export default function MailPage() {
  usePageMeta('Email Delivery | Aurora Admin', 'Inspect email consumer runtime aggregation.')

  // Đọc Zone ID đang hoạt động từ RAM Cache (Global Zustand Store)
  const { activeZone } = useZoneStore()

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
      {/* [COMMENT]: Operational infrastructure chuyển sang Grafana; trang này chỉ còn business consumer read model. */}
      <div className="flex flex-col gap-6 md:flex-row md:items-center md:justify-between border-b border-border/40 pb-5">
        <div className="space-y-1.5">
          <h1 className="text-2xl font-bold tracking-tight text-foreground sm:text-3xl">Email Delivery</h1>
          <p className="text-xs text-muted-foreground max-w-2xl">
            Consumer runtime aggregation across organizations and workspaces.
          </p>
        </div>
        
        {/* Nhóm điều khiển nằm bên phải trên desktop */}
        <div className="flex flex-wrap items-center gap-3 self-start md:self-auto">
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
        </div>
      </div>

      <div className="transition-all duration-300 ease-in-out">
        <ConsumersTab zoneID={activeZone} dateRange={dateRange} />
      </div>
    </div>
  )
}
