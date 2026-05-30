import { useState, useCallback, useMemo, useEffect, type ReactNode } from 'react'
import { Fetch } from '@/lib/fetch'
import { cn } from '@/lib/utils'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableRow } from '@/components/ui/table'

// Nhập các component nhỏ hiển thị thông số Gateways
import { GatewayMetrics } from '../sections/GatewayMetrics'
import { Inventory } from '../sections/Inventory'

// Kiểu dữ liệu phản hồi API tổng quát
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
 * Hiệu ứng Skeleton chờ khi đang tải danh sách Gateways
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

    const timeoutID = window.setTimeout(() => {
      void load()
      if (poll) {
        intervalID = window.setInterval(() => {
          void load().then((ok) => {
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

// Cấu trúc dữ liệu chi tiết phản hồi của Gateway Aggregation
type GatewayAggregationResponse = {
  metrics: { total_gateways: number; active_gateways: number; disabled_gateways: number; ready_shards: number; pending_shards: number; draining_shards: number; bound_endpoints: number }
  status: Array<{ status: string; count: number }> | null
  shard_states: Array<{ state: string; count: number; lag: number }> | null
  traffic_classes: Array<{ traffic_class: string; total_gateways: number; active_gateways: number; ready_shards: number; endpoint_count: number }> | null
  alerts: Array<{ title: string; gateway_id: string; gateway_name: string; severity: string; note: string; updated_at: string }> | null
  items: GatewayListItem[] | null
}

type GatewayListItem = { id: string; workspace_id: string; tenant_id: string; zone_id: string; name: string; traffic_class: string; status: string; routing_mode: string; desired_shard_count: number; endpoint_count: number; ready_shards: number; pending_shards: number; draining_shards: number; updated_at: string }

// Biểu đồ hình khuyên biểu diễn phân bố trạng thái Gateway
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

interface GatewaysTabProps {
  zoneID: string | null
}

/**
 * Tab Gateways chứa các biểu đồ phân bố, danh sách cảnh báo, thông số phân mảnh Shard và Fleet Inventory của Gateways.
 */
export function GatewaysTab({ zoneID }: GatewaysTabProps) {
  // Callback gọi tải dữ liệu Gateway từ API
  const loadGateways = useCallback(async (signal: AbortSignal) => {
    const params = new URLSearchParams()
    if (zoneID) params.set('zone_id', zoneID)
    const query = params.toString()
    const aggregation = await Fetch(`/admin/smtp/aggregation/gateways${query ? `?${query}` : ''}`, { signal }).then((resp) => readAPIData<GatewayAggregationResponse>(resp))
    return { aggregation, items: aggregation.items ?? [] }
  }, [zoneID])

  // Kích hoạt custom hook polling thăm dò dữ liệu realtime
  const state = usePollingResource(loadGateways)

  const formatSegmentValue = (value: number, total: number) => {
    const percent = total > 0 ? (value / total) * 100 : 0
    return `${formatNumber(value)} (${percent.toFixed(1)}%)`
  }

  // Chuyển đổi trạng thái Gateway thành dạng vẽ của Donut chart
  const statusSegmentsFromCounts = useCallback((rows: Array<{ status: string; count: number }>, total: number) => {
    const colors: Record<string, string> = { active: '#22c55e', draining: '#f59e0b', disabled: '#ef4444' }
    const source = rows.length > 0 ? rows : [{ status: 'empty', count: 0 }]
    return source.map((row) => ({
      label: statusLabel(row.status),
      value: formatSegmentValue(row.count, total),
      color: colors[row.status] ?? '#94a3b8',
    }))
  }, [])

  // Chuyển đổi trạng thái Shard của Gateway sang dạng Donut chart
  const gatewayShardSegments = useCallback((rows: Array<{ state: string; count: number }>, total: number) => {
    const colors: Record<string, string> = { active: '#22c55e', pending: '#f59e0b', draining: '#ef4444', disabled: '#94a3b8' }
    const source = rows.length > 0 ? rows : [{ state: 'empty', count: 0 }]
    return source.map((row) => ({
      label: statusLabel(row.state),
      value: formatSegmentValue(row.count, total),
      color: colors[row.state] ?? '#94a3b8',
    }))
  }, [])

  if (state.loading) return <OverviewSkeleton />
  if (state.error || !state.data) {
    return (
      <Panel title="Mail Gateways unavailable">
        <EmptyState title="Cannot load Mail gateways" description={state.error || 'Gateway data is unavailable.'} />
      </Panel>
    )
  }

  const { aggregation, items } = state.data
  const metrics = aggregation.metrics
  const shardTotal = metrics.ready_shards + metrics.pending_shards + metrics.draining_shards
  const statusTotal = (aggregation.status ?? []).reduce((sum, item) => sum + item.count, 0)

  return (
    <div className="space-y-3">
      {/* 1. Chỉ báo trạng thái tổng quan của Gateways */}
      <GatewayMetrics metrics={metrics} />

      {/* 2. Lưới biểu đồ Donut phân bố Gateways, Shards và bảng thông số Traffic Classes */}
      <div className="grid gap-3 xl:grid-cols-[.82fr_.82fr_1fr]">
        <Panel title="Gateway Status Distribution">
          <Donut total={formatNumber(statusTotal)} segments={statusSegmentsFromCounts(aggregation.status ?? [], statusTotal)} />
        </Panel>
        <Panel title="Shard State Distribution">
          <Donut total={formatNumber(shardTotal)} centerLabel="Total Shards" segments={gatewayShardSegments(aggregation.shard_states ?? [], shardTotal)} />
        </Panel>
        <Panel title="Traffic Class Summary">
          <CompactTable
            rows={(aggregation.traffic_classes ?? []).map((row) => [
              row.traffic_class,
              formatNumber(row.total_gateways),
              formatNumber(row.active_gateways),
              formatNumber(row.ready_shards),
            ])}
          />
        </Panel>
      </div>

      {/* 3. Lưới hiển thị các Alerts cảnh báo hệ thống cùng Fleet Inventory của Gateways */}
      <div className="grid gap-3 xl:grid-cols-[1fr_1.8fr]">
        <Panel title="Gateway Alerts">
          {(aggregation.alerts ?? []).length === 0 ? (
            <EmptyState title="No active alerts" description="All gateways are reporting healthy heartbeat signals." />
          ) : (
            <CompactTable
              rows={(aggregation.alerts ?? []).map((row) => [
                row.title,
                row.gateway_name,
                row.severity,
                formatRelativeTime(row.updated_at),
              ])}
            />
          )}
        </Panel>
        <Inventory
          title="Gateway Fleet Inventory"
          rows={items.map((row) => [
            row.name,
            row.tenant_id || 'All Organizations',
            row.zone_id || '-',
            row.traffic_class,
            row.routing_mode,
            formatNumber(row.ready_shards),
            formatNumber(row.pending_shards),
            formatNumber(row.draining_shards),
            statusLabel(row.status),
            formatRelativeTime(row.updated_at),
          ])}
          columns={['Name', 'Organization Scope', 'Zone', 'Traffic Class', 'Routing Mode', 'Ready', 'Pending', 'Draining', 'Status', 'Updated']}
        />
      </div>
    </div>
  )
}
