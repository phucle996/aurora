import { useState, useCallback, useEffect, type ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import { Pause, Play, Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Fetch } from '@/lib/fetch'
import { Skeleton } from '@/components/ui/skeleton'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

// Nhập Dialog kiểm tra kết nối SMTP
import { TestConnectionDialog } from '../sections/TestConnectionDialog'

// Kiểu dữ liệu phản hồi API chuẩn
type APIResponse<T = unknown> = {
  data?: T
  message?: string
  error?: string
}

// Đọc dữ liệu từ API ném ra lỗi nếu có trục trặc
async function readAPIData<T>(resp: Response): Promise<T> {
  if (!resp.ok) throw new Error('Cannot load Mail data.')
  const body = (await resp.json()) as APIResponse<T>
  if (!body.data) throw new Error('Mail response is empty.')
  return body.data
}

// Đọc thông điệp lỗi hoặc phản hồi từ API
async function readAPIMessage(resp: Response, fallback: string): Promise<string> {
  const body = (await resp.json().catch(() => null)) as APIResponse<unknown> | null
  const message = body?.message?.trim()
  const error = body?.error?.trim()

  if (message && message.toLowerCase() !== 'internal server error' && message.toLowerCase() !== 'service unavailable') {
    return message
  }
  if (error) {
    return error
  }
  if (message) {
    return message
  }
  return fallback
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
 * Hiệu ứng Skeleton chờ khi đang tải danh sách Endpoints
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


// Chuẩn hóa trạng thái hệ thống thành dạng viết hoa chữ đầu cách nhau (Ví dụ: "active" -> "Active")
function statusLabel(value: string) {
  if (!value) return '-'
  return value.split('_').map((part) => part.charAt(0).toUpperCase() + part.slice(1)).join(' ')
}

// Badge hiển thị màu sắc tương ứng với trạng thái Endpoint
function StatusBadge({ value }: { value: string }) {
  const isGood = value === 'Healthy' || value === 'Active' || value === 'Enabled' || value === 'delivered'
  const isWarn = value === 'Degraded' || value === 'Suspended' || value === 'dead_letter'
  return (
    <Badge
      variant="secondary"
      className={cn(
        'aurora-caption font-semibold px-2 py-0.5',
        isGood
          ? 'bg-emerald-500/10 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-400'
          : isWarn
            ? 'bg-amber-500/10 text-amber-700 dark:bg-amber-500/20 dark:text-amber-400'
            : 'bg-slate-500/10 text-slate-700 dark:bg-slate-500/20 dark:text-slate-400'
      )}
    >
      {value}
    </Badge>
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

type EndpointListItem = {
  id: string
  zone_id: string
  name: string
  provider: string
  connection_config: mapStringAny
  is_active: boolean
  created_at?: string
  updated_at?: string
  // Các thuộc tính bổ sung map để hiển thị ở frontend dễ dàng
  host?: string
  port?: number
  max_connections?: number
  tls_mode?: string
  has_secret?: boolean
  status?: string
}

type mapStringAny = {
  host?: string
  address?: string
  port?: number
  max_connections?: number
  pool_size?: number
  tls_mode?: string
  password?: string
  api_key?: string
  client_key_pem?: string
}

interface EndpointsTabProps {
  zoneID: string | null
}

/**
 * Tab Endpoints quản lý đội ngũ các Endpoints gửi thư SMTP (Endpoint Fleet).
 * Hỗ trợ các hành động thiết yếu bao gồm: tạo mới, chỉnh sửa cấu hình, kích hoạt/tạm ngưng nhanh, xóa và kết nối thử nghiệm thời gian thực.
 */
export function EndpointsTab({ zoneID }: EndpointsTabProps) {
  // refreshKey được thay đổi để buộc gọi lại hàm fetch tải lại dữ liệu mới nhất
  const [refreshKey, setRefreshKey] = useState(0)

  // Quản lý trạng thái cửa sổ hội thoại kiểm tra kết nối thử SMTP
  const [connState, setConnState] = useState<{
    isOpen: boolean
    loading: boolean
    success: boolean | null
    message: string
    endpointName: string
  }>({
    isOpen: false,
    loading: false,
    success: null,
    message: '',
    endpointName: '',
  })

  // Khai báo hàm fetch tải danh sách SMTP Endpoints từ API
  const loadEndpoints = useCallback(async (signal: AbortSignal) => {
    void refreshKey
    if (!zoneID) return { items: [] }
    const queryParams = `?zone_id=${zoneID}`
    const list = await Fetch(`/admin/mail/endpoints${queryParams}`, { signal }).then((resp) => readAPIData<EndpointListItem[]>(resp))

    // Ánh xạ lại các cấu hình bên trong connection_config ra ngoài phẳng (flat object) để dễ vẽ bảng
    const items = (list ?? []).map(ep => {
      const cfg = ep.connection_config || {}
      return {
        ...ep,
        host: String(cfg.host || cfg.address || '-'),
        port: Number(cfg.port || 0),
        max_connections: Number(cfg.max_connections || cfg.pool_size || 10),
        tls_mode: String(cfg.tls_mode || 'none'),
        has_secret: !!(cfg.password || cfg.api_key || cfg.client_key_pem),
        status: ep.is_active ? 'active' : 'disabled',
      }
    })

    return { items }
  }, [refreshKey, zoneID])

  // Sử dụng custom hook polling để nạp thông tin
  const state = usePollingResource(loadEndpoints, { poll: false })

  if (!zoneID) {
    return (
      <Panel title="Endpoint Fleet">
        <EmptyState
          title="No Zone Selected"
          description="Please select a specific Zone from the filter dropdown above to view and manage Mail Endpoints."
        />
      </Panel>
    )
  }

  // Gọi API thực hiện xóa một Endpoint SMTP
  const deleteEndpoint = async (endpoint: EndpointListItem) => {
    if (!window.confirm(`Delete endpoint ${endpoint.name}?`)) return
    const queryParams = `?zone_id=${zoneID}`
    const resp = await Fetch(`/admin/mail/endpoints/${endpoint.id}${queryParams}`, { method: 'DELETE' })
    if (!resp.ok) {
      window.alert(await readAPIMessage(resp, 'Cannot delete endpoint.'))
      return
    }
    setRefreshKey((value) => value + 1)
  }


  // Gọi API kiểm tra kết nối SMTP Server trực tiếp realtime
  const tryConnect = async (endpoint: EndpointListItem) => {
    setConnState({
      isOpen: true,
      loading: true,
      success: null,
      message: 'Establishing connection to Mail server...',
      endpointName: endpoint.name,
    })

    try {
      const queryParams = `?zone_id=${zoneID}`
      const resp = await Fetch(`/admin/mail/endpoints/${endpoint.id}/test-connect${queryParams}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
      })

      if (resp.ok) {
        setConnState(prev => ({
          ...prev,
          loading: false,
          success: true,
          message: 'Connection successful! The Mail server is reachable and credentials are valid.',
        }))
      } else {
        const failureMessage = await readAPIMessage(resp, 'Connection failed. Please check your host, port, and credentials.')
        setConnState(prev => ({
          ...prev,
          loading: false,
          success: false,
          message: failureMessage,
        }))
      }
    } catch (error) {
      setConnState(prev => ({
        ...prev,
        loading: false,
        success: false,
        message: error instanceof Error ? error.message : 'An unexpected error occurred while connecting.',
      }))
    }
  }

  // Thay đổi trạng thái kích hoạt/tạm ngưng của Endpoint nhanh từ bảng danh sách
  const updateStatus = async (endpoint: EndpointListItem, isActive: boolean) => {
    try {
      const queryParams = `?zone_id=${zoneID}`
      const resp = await Fetch(`/admin/mail/endpoints/${endpoint.id}${queryParams}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: endpoint.name,
          connection_config: endpoint.connection_config,
          is_active: isActive,
        }),
      })
      if (!resp.ok) throw new Error(await readAPIMessage(resp, 'Cannot update status.'))
      setRefreshKey((v) => v + 1)
    } catch (err) {
      console.error(err)
      alert(err instanceof Error ? err.message : 'Cannot update status.')
    }
  }

  if (state.loading) {
    return (
      <div className="space-y-4">
        <OverviewSkeleton />
      </div>
    )
  }

  if (state.error || !state.data) {
    return (
      <Panel title="Mail Endpoints unavailable">
        <EmptyState title="Cannot load Mail endpoints" description={state.error || 'Endpoint data is unavailable.'} />
      </Panel>
    )
  }

  const items = state.data.items

  return (
    <div className="space-y-3">
      {/* Khung Fleet Panel chứa bảng danh sách và nút Thêm mới */}
      <Panel
        title="Endpoint Fleet"
        action={
          <Button asChild className="h-9 font-bold cursor-pointer">
            <Link to="/mail/endpoints/new">
              <Plus className="size-4 mr-1.5" />
              Add Endpoint
            </Link>
          </Button>
        }
      >
        {items.length === 0 ? (
          <EmptyState title="No Mail endpoints" description="Create an endpoint to start routing Mail delivery." />
        ) : (
          <div className="rounded-xl border border-border/60 bg-muted/10 overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow className="bg-muted/40 hover:bg-muted/40">
                  <TableHead className="font-semibold text-foreground/80">Name</TableHead>
                  <TableHead className="font-semibold text-foreground/80">Host</TableHead>
                  <TableHead className="font-semibold text-foreground/80">TLS</TableHead>
                  <TableHead className="font-semibold text-foreground/80">Capacity</TableHead>
                  <TableHead className="font-semibold text-foreground/80">Secret</TableHead>
                  <TableHead className="font-semibold text-foreground/80">Status</TableHead>
                  <TableHead className="text-right font-semibold text-foreground/80">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((item) => (
                  <TableRow key={item.id} className="hover:bg-muted/10 transition-colors">
                    {/* Tên Endpoint được làm đậm tạo điểm nhấn */}
                    <TableCell className="font-bold py-3">{item.name}</TableCell>
                    {/* Địa chỉ Host và Cổng kết nối */}
                    <TableCell className="py-3">
                      {item.host}:{item.port}
                    </TableCell>
                    {/* Chế độ mã hóa TLS */}
                    <TableCell className="py-3">{statusLabel(item.tls_mode || 'none')}</TableCell>
                    {/* Sức chứa tối đa (Max Connections) */}
                    <TableCell className="py-3">{formatNumber(item.max_connections || 10)}</TableCell>
                    {/* Trạng thái cấu hình mật khẩu/khoá bảo mật */}
                    <TableCell className="py-3">{item.has_secret ? 'Configured' : 'Missing'}</TableCell>
                    {/* Badge trạng thái hoạt động */}
                    <TableCell className="py-3">
                      <StatusBadge value={statusLabel(item.status || 'disabled')} />
                    </TableCell>
                    {/* Cột các hành động điều khiển dòng */}
                    <TableCell className="text-right py-3">
                      <div className="flex justify-end gap-2">
                        {/* Nút sửa thông tin */}
                        <Button asChild variant="outline" size="sm" className="cursor-pointer">
                          <Link to="/mail/endpoints/$id/edit" params={{ id: item.id }}>Edit</Link>
                        </Button>
                        {/* Nút chuyển đổi nhanh trạng thái kích hoạt / tạm ngưng */}
                        {!item.is_active && (
                          <Button variant="outline" size="icon-sm" onClick={() => void updateStatus(item, true)} title="Activate" className="cursor-pointer">
                            <Play className="size-4 text-emerald-500 fill-emerald-500/20" />
                          </Button>
                        )}
                        {item.is_active && (
                          <Button variant="outline" size="icon-sm" onClick={() => void updateStatus(item, false)} title="Suspend" className="cursor-pointer">
                            <Pause className="size-4 text-amber-500 fill-amber-500/20" />
                          </Button>
                        )}
                        {/* Nút kiểm tra kết nối trực tiếp tại chỗ */}
                        <Button variant="outline" size="sm" onClick={() => void tryConnect(item)} className="cursor-pointer">
                          Try
                        </Button>
                        {/* Nút xóa Endpoint */}
                        <Button variant="destructive" size="icon-sm" onClick={() => void deleteEndpoint(item)} aria-label={`Delete ${item.name}`} className="cursor-pointer">
                          <Trash2 className="size-4" />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </Panel>

      {/* Cửa sổ hội thoại nổi thông báo kết quả kiểm tra kết nối */}
      <TestConnectionDialog
        isOpen={connState.isOpen}
        onOpenChange={(open) => setConnState(prev => ({ ...prev, isOpen: open }))}
        loading={connState.loading}
        success={connState.success}
        message={connState.message}
        endpointName={connState.endpointName}
      />
    </div>
  )
}
