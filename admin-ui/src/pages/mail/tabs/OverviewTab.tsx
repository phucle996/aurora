import { useEffect, useState, type ReactNode } from 'react'
import { type DateRange } from 'react-day-picker'
import { Fetch } from '@/lib/fetch'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

// Nhập các thành phần giao diện nhỏ trong Overview Tab
import { OverviewMetrics } from '../sections/OverviewMetrics'
import { ThroughputChart } from '../sections/ThroughputChart'
import { HealthDistribution } from '../sections/HealthDistribution'
import { TopOrganizations } from '../sections/TopOrganizations'
import { ZoneHealthSummary } from '../sections/ZoneHealthSummary'
import { RecentIncidents } from '../sections/RecentIncidents'
import { OperationalInsights } from '../sections/OperationalInsights'

// Cấu trúc APIResponse tổng quát
type APIResponse<T = unknown> = {
  data?: T
  message?: string
  error?: string
}

/**
 * Component Panel bọc nội dung và tiêu đề
 */
function Panel({
  title,
  action,
  children,
  className,
}: {
  title: string
  action?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={cn('rounded-xl border border-border bg-card p-4 shadow-sm', className)}>
      <div className="mb-4 flex items-center justify-between gap-3">
        <h3 className="aurora-section-title">{title}</h3>
        {action}
      </div>
      {children}
    </section>
  )
}

/**
 * Component EmptyState hiển thị khi không có dữ liệu để xem
 */
function EmptyState({ title, description }: { title: string; description: string }) {
  return (
    <div className="rounded-lg border border-dashed border-border bg-muted/30 p-6 text-center">
      <p className="aurora-insight-title">{title}</p>
      <p className="mt-1 aurora-insight-meta">{description}</p>
    </div>
  )
}

/**
 * Component hiển thị bộ xương chờ (Skeleton Loading) đặc trưng khi dữ liệu tổng quan đang được load từ API
 */
function OverviewSkeleton() {
  return (
    <div className="grid gap-3 xl:grid-cols-4">
      {Array.from({ length: 4 }).map((_, index) => (
        <Skeleton key={index} className="h-28 rounded-xl border border-border" />
      ))}
    </div>
  )
}

// Cấu trúc kiểu dữ liệu trả về từ API SMTP Aggregation tổng hợp
type SMTPOverviewResponse = {
  metrics: {
    delivered_today: number
    queued_now: number
    active_consumers: number
    total_consumers: number
    active_gateways: number
    total_gateways: number
    delivery_success_rate: number
  }
  delivery_throughput: Array<{ label: string; delivered: number; queued: number; retries: number }> | null
  health_distribution: { healthy: number; warning: number; stopped: number; unknown: number }
  top_organizations: Array<{ tenant_id: string; delivered: number; total_attempts: number; success_rate: number; queued: number }> | null
  zone_health: Array<{ zone_id: string; healthy: number; degraded: number; unhealthy: number; total: number; status: string }> | null
  timeline: Array<{ id: string; entity_type: string; entity_name: string; action: string; actor_name: string; note: string; created_at: string }> | null
  insights: Array<{ title: string; value: string; note: string; tone: string }> | null
}

// Định nghĩa State lưu trữ trạng thái tổng quan
type OverviewState = {
  loading: boolean
  error: string
  data: SMTPOverviewResponse | null
}

interface OverviewTabProps {
  zoneID: string | null             // Vùng lọc được truyền từ trang cha MailPage
  dateRange: DateRange | undefined  // Khoảng thời gian lọc được truyền từ trang cha
}

/**
 * Tab Overview hiển thị bức tranh toàn cảnh về lưu lượng gửi thư, sức khỏe, phân bổ vùng địa lý và các log sự cố gần đây.
 * Áp dụng cơ chế AbortController để chủ động hủy các yêu cầu mạng lỗi thời khi người dùng chuyển đổi bộ lọc liên tục.
 */
export function OverviewTab({ zoneID, dateRange }: OverviewTabProps) {
  // Quản lý trạng thái tải dữ liệu tổng quan
  const [state, setState] = useState<OverviewState>({ loading: true, error: '', data: null })

  // Lắng nghe sự thay đổi của zoneID hoặc dateRange để kích hoạt tải dữ liệu tương ứng
  useEffect(() => {
    let cancelled = false
    const controller = new AbortController()

    async function loadOverview() {
      setState((prev) => ({ ...prev, loading: true, error: '' }))
      try {
        // Xây dựng chuỗi tham số truy vấn tìm kiếm
        const params = new URLSearchParams()
        if (zoneID) params.set('zone_id', zoneID)
        if (dateRange?.from) params.set('start_at', dateRange.from.toISOString())
        if (dateRange?.to) params.set('end_at', dateRange.to.toISOString())

        const query = params.toString()
        const url = `/admin/smtp/aggregation${query ? `?${query}` : ''}`

        // Thực hiện Fetch dữ liệu kèm theo signal để phục vụ hủy luồng mạng
        const resp = await Fetch(url, { signal: controller.signal })
        if (!resp.ok) {
          throw new Error('Cannot load Mail overview.')
        }
        const body = (await resp.json()) as APIResponse<SMTPOverviewResponse>
        if (!body.data) {
          throw new Error('Mail overview response is empty.')
        }
        if (!cancelled) {
          setState({ loading: false, error: '', data: body.data })
        }
      } catch (error) {
        if (error instanceof DOMException && error.name === 'AbortError') return
        if (!cancelled) {
          setState({ loading: false, error: error instanceof Error ? error.message : 'Cannot load Mail overview.', data: null })
        }
      }
    }

    // Thiết lập timeout gọi tải dữ liệu để tránh hiện tượng chặn luồng chính (non-blocking)
    const timeoutID = window.setTimeout(() => void loadOverview(), 0)
    return () => {
      cancelled = true
      controller.abort()
      window.clearTimeout(timeoutID)
    }
  }, [zoneID, dateRange])

  // Trả về giao diện skeleton chờ khi dữ liệu đang được tải
  if (state.loading) {
    return <OverviewSkeleton />
  }

  // Giao diện thông báo lỗi nếu tải dữ liệu thất bại từ phía máy chủ
  if (state.error || !state.data) {
    return (
      <Panel title="Mail Overview unavailable">
        <EmptyState title="Cannot load Mail overview" description={state.error || 'Mail overview data is unavailable.'} />
      </Panel>
    )
  }

  const overview = state.data

  return (
    <div className="space-y-3">
      {/* 1. Lưới hiển thị các metrics đếm số lượng nhanh */}
      <OverviewMetrics metrics={overview.metrics} />
      
      {/* 2. Lưới hiển thị Biểu đồ Thông lượng, Biểu đồ sức khỏe và Top Tổ chức lưu lượng cao */}
      <div className="grid gap-3 xl:grid-cols-[1.4fr_.72fr_1.12fr]">
        <ThroughputChart throughput={overview.delivery_throughput ?? []} />
        <HealthDistribution health={overview.health_distribution} />
        <TopOrganizations organizations={overview.top_organizations ?? []} />
      </div>
      
      {/* 3. Lưới hiển thị Phân bố Vùng hạ tầng, Lịch sử thay đổi/sự cố và Đánh giá phân tích vận hành */}
      <div className="grid gap-3 xl:grid-cols-3">
        <ZoneHealthSummary zones={overview.zone_health ?? []} />
        <RecentIncidents timeline={overview.timeline ?? []} />
        <OperationalInsights insights={overview.insights ?? []} />
      </div>
    </div>
  )
}
