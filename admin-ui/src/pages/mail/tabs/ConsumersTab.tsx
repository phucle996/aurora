import { useState, useCallback, useMemo, useEffect, type ReactNode } from 'react'
import { type DateRange } from 'react-day-picker'
import { Fetch } from '@/lib/fetch'
import { cn } from '@/lib/utils'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableRow } from '@/components/ui/table'

// Nhập các component nhỏ hiển thị thông số Consumers
import { ConsumerMetrics } from '../sections/ConsumerMetrics'
import { Inventory } from '../sections/Inventory'

// Kiểu dữ liệu phản hồi API chuẩn
type APIResponse<T = unknown> = {
  data?: T
  message?: string
  error?: string
}

// Đọc dữ liệu dữ liệu từ API ném ra lỗi nếu có trục trặc
async function readAPIData<T>(resp: Response): Promise<T> {
  if (!resp.ok) throw new Error('Cannot load Mail data.')
  const body = (await resp.json()) as APIResponse<T>
  if (!body.data) throw new Error('Mail response is empty.')
  return body.data
}

/**
 * Component Panel bọc các phần tiêu đề và nội dung
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
 * Component EmptyState hiển thị khi không có dữ liệu
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
 * Hiệu ứng Skeleton chờ khi đang tải danh sách Consumers
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

// Định dạng hiển thị số nguyên
function formatNumber(value: number) {
  return new Intl.NumberFormat('en-US').format(value)
}

// Định dạng số dạng rút gọn (ví dụ: 1.2K, 3.4M) giúp tối ưu diện tích hiển thị trên các màn hình nhỏ
function formatCompactNumber(value: number) {
  return new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 2 }).format(value)
}

// Tính thời gian tương đối so với hiện tại để dễ quan sát (Ví dụ: "10m ago")
function formatRelativeTime(input: string) {
  const value = new Date(input).getTime()
  if (!Number.isFinite(value)) return ''
  const diff = Math.max(Date.now() - value, 0)
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

// Chuẩn hóa trạng thái hệ thống thành dạng viết hoa chữ đầu cách nhau (Ví dụ: "active_draining" -> "Active Draining")
function statusLabel(value: string) {
  if (!value) return '-'
  return value.split('_').map((part) => part.charAt(0).toUpperCase() + part.slice(1)).join(' ')
}

// Xác định màu sắc hiển thị phù hợp với từng trạng thái/từ khóa sức khỏe
function statusClass(value: string) {
  return value === 'Healthy' || value === 'Active'
    ? 'text-emerald-600 dark:text-emerald-400 font-semibold'
    : value === 'Degraded' || value === 'Medium' || value === 'Suspended'
      ? 'text-amber-600 dark:text-amber-400'
      : value === 'High' || value === 'Unhealthy'
        ? 'text-destructive font-semibold'
        : ''
}

/**
 * Component bảng hiển thị dạng thu gọn đơn giản
 */
function CompactTable({ rows }: { rows: string[][] }) {
  return (
    <Table>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.join('-')} className="h-9">
            {row.map((cell, index) => (
              <TableCell
                key={index}
                className={cn(
                  'aurora-table-cell',
                  index === 0 && 'aurora-table-key',
                  index === row.length - 1 && statusClass(cell)
                )}
              >
                {cell}
              </TableCell>
            ))}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

type ResourceState<T> = { loading: boolean; error: string; data: T | null }

/**
 * Hook tùy biến hỗ trợ Polling (gọi lại API theo chu kỳ 15s) hỗ trợ SRE xem dữ liệu liên tục realtime.
 * Tự động tạm dừng gọi khi tab trình duyệt bị ẩn (document.hidden) giúp tối ưu hóa tài nguyên mạng của Cloud HA.
 */
function usePollingResource<T>(
  loader: (signal: AbortSignal) => Promise<T>,
  options: { poll?: boolean } = {}
): ResourceState<T> {
  const [state, setState] = useState<ResourceState<T>>({ loading: true, error: '', data: null })
  const poll = options.poll ?? true

  useEffect(() => {
    let cancelled = false
    let intervalID: number | undefined
    let controller: AbortController | undefined

    async function load(): Promise<boolean> {
      // Bỏ qua nếu tab trình duyệt đang bị ẩn
      if (document.hidden) return true
      controller?.abort()
      controller = new AbortController()
      try {
        const data = await loader(controller.signal)
        if (!cancelled) setState({ loading: false, error: '', data })
        return true
      } catch (error) {
        if (error instanceof DOMException && error.name === 'AbortError') return true
        if (!cancelled) {
          setState({
            loading: false,
            error: error instanceof Error ? error.message : 'Cannot load Mail data.',
            data: null,
          })
        }
        return false
      }
    }

    // Thiết lập tiến trình vòng lặp gọi API
    const timeoutID = window.setTimeout(() => {
      void load()
      if (poll) {
        intervalID = window.setInterval(() => {
          void load().then((ok) => {
            // Nếu phát hiện lỗi mạng kéo dài thì chuyển sang chu kỳ thăm dò chậm hơn (30s) tránh spam server
            if (!ok && intervalID !== undefined) {
              window.clearInterval(intervalID)
              intervalID = window.setInterval(() => void load(), 30_000)
            }
          })
        }, 15_000)
      }
    }, 0)

    return () => {
      cancelled = true
      controller?.abort()
      if (timeoutID !== undefined) window.clearTimeout(timeoutID)
      if (intervalID !== undefined) window.clearInterval(intervalID)
    }
  }, [loader, poll])

  return state
}

// Cấu trúc dữ liệu chi tiết phản hồi của Consumer Aggregation
type ConsumerAggregationResponse = {
  metrics: { total_consumers: number; active_consumers: number; disabled_consumers: number; total_lag: number; avg_worker_concurrency: number; avg_ack_timeout_seconds: number }
  status: Array<{ status: string; count: number }> | null
  shard_states: Array<{ state: string; count: number; lag: number }> | null
  organizations: Array<{ tenant_id: string; label: string; total: number; active: number; disabled: number; total_lag: number; workspace_id: string }> | null
  workspace_summary: { workspace_id: string; total_consumers: number; active_consumers: number; disabled_consumers: number; total_lag: number; tenant_count: number } | null
  lagging_consumers: Array<{ id: string; name: string; tenant_id: string; lag: number; status: string; updated_at: string }> | null
  items: ConsumerListItem[] | null
}

type ConsumerListItem = { id: string; workspace_id: string; tenant_id: string; zone_id: string; name: string; transport_type: string; source: string; consumer_group: string; worker_concurrency: number; desired_shard_count: number; lag: number; status: string; updated_at: string }

// Biểu đồ hình khuyên biểu diễn phân bố trạng thái Consumer
function Donut({ total, segments, centerLabel = 'Total' }: { total: string; segments: Array<{ label: string; value: string; color: string }>; centerLabel?: string }) {
  const gradient = useMemo(() => {
    const step = 100 / segments.length
    return segments.map((s, i) => `${s.color} ${i * step}% ${(i + 1) * step}%`).join(', ')
  }, [segments])
  return (
    <div className="flex flex-col items-center justify-center gap-6 sm:flex-row sm:gap-8 h-full min-h-57.5">
      <div className="relative size-36 rounded-full shrink-0" style={{ background: `conic-gradient(${gradient})` }}>
        <div className="absolute inset-7 flex flex-col items-center justify-center rounded-full bg-card shadow-inner">
          <span className="aurora-metric-value">{total}</span>
          <span className="text-xs font-medium text-muted-foreground">{centerLabel}</span>
        </div>
      </div>
      <div className="space-y-2 text-sm w-full max-w-50">
        {segments.map((segment) => (
          <div key={segment.label} className="flex items-center justify-between gap-4">
            <span className="inline-flex items-center gap-2 font-medium text-muted-foreground">
              <span className="size-2 rounded-full" style={{ background: segment.color }} />
              {segment.label}
            </span>
            <span className="font-semibold text-foreground">{segment.value}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

interface ConsumersTabProps {
  zoneID: string | null
  dateRange: DateRange | undefined
}

/**
 * Tab Consumers chứa danh sách các Consumers, thông số Shard, hàng đợi lag và thông số chi tiết của Consumer Fleet.
 * Hỗ trợ chuyển đổi nhanh phạm vi hiển thị theo Tổ chức hoặc theo Không gian làm việc.
 */
export function ConsumersTab({ zoneID, dateRange }: ConsumersTabProps) {
  // State quản lý phạm vi xem thông số (Tổ chức hoặc Không gian làm việc)
  const [scope, setScope] = useState<'organization' | 'workspace'>('organization')

  // Hàm callback gọi tải dữ liệu Consumers từ API
  const loadConsumers = useCallback(async (signal: AbortSignal) => {
    const params = new URLSearchParams()
    if (zoneID) params.set('zone_id', zoneID)
    if (dateRange?.from) params.set('start_at', dateRange.from.toISOString())
    if (dateRange?.to) params.set('end_at', dateRange.to.toISOString())
    const query = params.toString()

    const aggregation = await Fetch(`/admin/smtp/aggregation/consumers${query ? `?${query}` : ''}`, { signal }).then((resp) => readAPIData<ConsumerAggregationResponse>(resp))
    return { aggregation, items: aggregation.items ?? [] }
  }, [zoneID, dateRange])

  // Gọi thăm dò dữ liệu liên tục qua custom hook polling
  const state = usePollingResource(loadConsumers)

  const formatSegmentValue = (value: number, total: number) => {
    const percent = total > 0 ? (value / total) * 100 : 0
    return `${formatNumber(value)} (${percent.toFixed(1)}%)`
  }

  // Chuyển đổi định dạng trạng thái Consumer sang dạng vẽ của Donut chart
  const statusSegmentsFromCounts = useCallback((rows: Array<{ status: string; count: number }>, total: number) => {
    const colors: Record<string, string> = { active: '#22c55e', draining: '#f59e0b', disabled: '#ef4444' }
    const source = rows.length > 0 ? rows : [{ status: 'empty', count: 0 }]
    return source.map((row) => ({
      label: statusLabel(row.status),
      value: formatSegmentValue(row.count, total),
      color: colors[row.status] ?? '#94a3b8',
    }))
  }, [])

  // Chuyển đổi trạng thái Shard sang dạng danh sách các hàng hiển thị
  const consumerShardRows = useCallback((rows: Array<{ state: string; count: number; lag: number }>) => {
    if (rows.length === 0) return [['No shards yet', '0', '0']]
    return rows.map((row) => [statusLabel(row.state), formatNumber(row.count), formatCompactNumber(row.lag)])
  }, [])

  if (state.loading) return <OverviewSkeleton />
  if (state.error || !state.data) {
    return (
      <Panel title="Mail Consumers unavailable">
        <EmptyState title="Cannot load Mail consumers" description={state.error || 'Consumer data is unavailable.'} />
      </Panel>
    )
  }

  const { aggregation, items } = state.data
  const metrics = aggregation.metrics
  const statusTotal = (aggregation.status ?? []).reduce((sum, item) => sum + item.count, 0)
  const statusSegments = statusSegmentsFromCounts(aggregation.status ?? [], statusTotal)

  const orgRows = aggregation.organizations ?? []
  const orgTableRows = orgRows.map((row) => [
    row.label || row.tenant_id || 'No organization',
    formatNumber(row.total),
    formatNumber(row.active),
    formatNumber(row.disabled),
    formatCompactNumber(row.total_lag),
  ])

  return (
    <div className="space-y-3">
      {/* 1. Thẻ chỉ số Consumers tổng hợp */}
      <ConsumerMetrics metrics={metrics} tenantCount={aggregation.workspace_summary?.tenant_count ?? 0} />
      
      {/* 2. Lưới thông số phân bổ theo tổ chức, trạng thái và trạng thái các Shards */}
      <div className="grid gap-3 xl:grid-cols-[1.2fr_.86fr_1fr]">
        <Panel title="Consumer Count by Organization">
          {orgTableRows.length === 0 ? (
            <EmptyState title="No consumer organizations" description="Consumers without tenant_id will appear as No organization when present." />
          ) : (
            <CompactTable rows={orgTableRows} />
          )}
        </Panel>
        <Panel title="Consumer Status Distribution">
          <Donut total={formatNumber(statusTotal)} segments={statusSegments} />
        </Panel>
        <Panel title="Consumer Shard States">
          <CompactTable rows={consumerShardRows(aggregation.shard_states ?? [])} />
        </Panel>
      </div>

      {/* 3. Phân vùng xem chi tiết theo phạm vi (Scope) */}
      <Panel
        title="Consumer Scope"
        action={
          <div className="flex rounded-lg border border-border bg-muted/50 p-1">
            <button
              className={cn('rounded-md px-3 py-1 text-xs font-semibold cursor-pointer transition-all', scope === 'organization' && 'bg-card text-primary shadow-sm')}
              onClick={() => setScope('organization')}
            >
              By Organization
            </button>
            <button
              className={cn('rounded-md px-3 py-1 text-xs font-semibold cursor-pointer transition-all', scope === 'workspace' && 'bg-card text-primary shadow-sm')}
              onClick={() => setScope('workspace')}
            >
              By Workspace
            </button>
          </div>
        }
      >
        {scope === 'organization' ? (
          orgTableRows.length === 0 ? (
            <EmptyState title="No consumer organizations" description="Consumers without tenant_id will appear as No organization when present." />
          ) : (
            <CompactTable rows={orgTableRows} />
          )
        ) : !aggregation.workspace_summary ? (
          <EmptyState title="No workspace summary" description="Workspace-scoped consumer totals will appear after data is created." />
        ) : (
          <CompactTable
            rows={[
              [
                aggregation.workspace_summary.workspace_id,
                formatNumber(aggregation.workspace_summary.total_consumers),
                formatNumber(aggregation.workspace_summary.active_consumers),
                formatNumber(aggregation.workspace_summary.disabled_consumers),
                formatCompactNumber(aggregation.workspace_summary.total_lag),
                formatNumber(aggregation.workspace_summary.tenant_count),
              ],
            ]}
          />
        )}
      </Panel>

      {/* 4. Danh sách các Consumers bị trễ xử lý (Lagging Consumers) và Toàn bộ danh mục fleet */}
      <div className="grid gap-3 xl:grid-cols-[1fr_1.8fr]">
        <Panel title="Lagging Consumers (by Highest Lag)">
          <CompactTable
            rows={(aggregation.lagging_consumers ?? []).map((row) => [
              row.name,
              row.tenant_id || 'No organization',
              formatCompactNumber(row.lag),
              statusLabel(row.status),
            ])}
          />
        </Panel>
        <Inventory
          title="Consumer Fleet Inventory"
          rows={items.map((row) => [
            row.name,
            row.tenant_id || 'No organization',
            row.source,
            row.transport_type,
            String(row.worker_concurrency),
            String(row.desired_shard_count),
            formatCompactNumber(row.lag),
            statusLabel(row.status),
            formatRelativeTime(row.updated_at),
          ])}
          columns={['Name', 'Organization', 'Source', 'Transport', 'Worker Concurrency', 'Desired Shards', 'Lag', 'Status', 'Updated']}
        />
      </div>
    </div>
  )
}
