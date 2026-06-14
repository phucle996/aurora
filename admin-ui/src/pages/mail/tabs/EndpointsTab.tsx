import { useState, useCallback, useEffect, useMemo } from 'react'
import { Link } from '@tanstack/react-router'
import { Pause, Play, Plus, Trash2, Search, ChevronLeft, ChevronRight, ChevronDown, RefreshCcw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Fetch } from '@/lib/fetch'
import { Skeleton } from '@/components/ui/skeleton'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
// Import thư viện Sonner Toast để hiển thị thông báo lỗi/thành công dạng toast notification (HA-friendly)
import { toast } from 'sonner'

// Nhập Dialog kiểm tra kết nối SMTP
import { TestConnectionDialog } from '../sections/TestConnectionDialog'

// Nhập hook kiểm soát phạm vi hoạt động (Global vs Zone)
import { useFeatureScope } from '@/hooks/useFeatureScope'

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

// Badge hiển thị màu sắc tương ứng với trạng thái Endpoint theo style ZoneTable
export function StatusBadge({ status }: { status: string }) {
  const isActive = status === 'active' || status === 'Active'
  return (
    <Badge
      variant="outline"
      className={cn(
        'h-8 rounded-lg border px-3 text-sm font-medium',
        isActive 
          ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-500/20 dark:bg-emerald-500/10 dark:text-emerald-400' 
          : 'border-slate-200 bg-slate-50 text-slate-600 dark:border-slate-800 dark:bg-slate-900/50 dark:text-slate-400'
      )}
    >
      {isActive ? 'Active' : 'Suspended'}
    </Badge>
  )
}

type ResourceState<T> = { loading: boolean; error: string; data: T | null }

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
  // connection_config được đánh dấu optional vì backend trả về cấu trúc phẳng (flat fields)
  connection_config?: mapStringAny
  is_active: boolean
  created_at?: string
  updated_at?: string
  host?: string
  port?: number
  max_connections?: number
  tls_mode?: string
  has_secret?: boolean
  status?: string
  // Bổ sung các trường cấu hình phẳng được API trả về trực tiếp để đồng bộ hóa DTO
  username?: string
  priority?: number
  weight?: number
  ca_cert_pem?: string
  client_cert_pem?: string
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
  zoneCode: string | null
}

export function EndpointsTab({ zoneCode }: EndpointsTabProps) {
  // Sử dụng declarative scope-matching hook để xác định quyền ghi trên tab endpoints
  const { canWrite } = useFeatureScope('endpoints')

  const [refreshKey, setRefreshKey] = useState(0)
  const [query, setQuery] = useState('')
  const [pageSize, setPageSize] = useState(8)
  const [currentPage, setCurrentPage] = useState(1)

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

  // Gọi API tải danh sách endpoints từ backend.
  // API thực tế trả về một object có dạng { items: [...], next_cursor: ... } ở trường "data".
  const loadEndpoints = useCallback(async (signal: AbortSignal) => {
    void refreshKey
    const zoneHeader = zoneCode ?? 'global'
    const listResponse = await Fetch(`/admin/mail/endpoints`, {
      signal,
      headers: { 'X-Zone-Code': zoneHeader },
    }).then((resp) => readAPIData<{ items: EndpointListItem[] }>(resp))

    // Trích xuất mảng dữ liệu thực tế từ listResponse.items
    const list = listResponse?.items ?? []

    // Map dữ liệu phẳng (flat properties) trả về trực tiếp từ backend API
    const items = list.map(ep => {
      return {
        ...ep,
        host: String(ep.host || '-'),
        port: Number(ep.port || 0),
        max_connections: Number(ep.max_connections || 10),
        tls_mode: String(ep.tls_mode || 'none'),
        has_secret: !!(ep.username || ep.client_cert_pem), // Xác định xem đã được cấu hình thông tin xác thực/chứng chỉ hay chưa
        status: ep.is_active ? 'active' : 'disabled',
      }
    })

    return { items }
  }, [refreshKey, zoneCode])

  const state = usePollingResource(loadEndpoints, { poll: false })

  // Lắng nghe và hiển thị lỗi load danh sách bằng toaster notification thay vì hiển thị trực tiếp trên UI
  useEffect(() => {
    if (state.error) {
      toast.error(`Cannot load endpoints: ${state.error}`)
    }
  }, [state.error])

  // Hàm xóa endpoint, nếu có lỗi sẽ hiển thị qua Sonner Toast thay vì alert
  const deleteEndpoint = async (endpoint: EndpointListItem) => {
    if (!window.confirm(`Delete endpoint ${endpoint.name}?`)) return
    try {
      const resp = await Fetch(`/admin/mail/endpoints/${endpoint.id}`, {
        method: 'DELETE',
        headers: { 'X-Zone-Code': zoneCode ?? 'global' },
      })
      if (!resp.ok) {
        toast.error(await readAPIMessage(resp, 'Cannot delete endpoint.'))
        return
      }
      toast.success('Mail endpoint deleted successfully!')
      setRefreshKey((value) => value + 1)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Cannot delete endpoint.')
    }
  }

  // Chức năng Test connection thủ công từ UI
  const tryConnect = async (endpoint: EndpointListItem) => {
    setConnState({
      isOpen: true,
      loading: true,
      success: null,
      message: 'Establishing connection to Mail server...',
      endpointName: endpoint.name,
    })

    try {
      const resp = await Fetch(`/admin/mail/endpoints/${endpoint.id}/test-connect`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Zone-Code': zoneCode ?? 'global',
        },
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

  // Hàm cập nhật trạng thái hoạt động (Active/Suspended) dùng cấu trúc phẳng (flat payload) tương thích DTO
  const updateStatus = async (endpoint: EndpointListItem, isActive: boolean) => {
    try {
      const resp = await Fetch(`/admin/mail/endpoints/${endpoint.id}`, {
        method: 'PATCH',
        headers: {
          'Content-Type': 'application/json',
          'X-Zone-Code': zoneCode ?? 'global',
        },
        body: JSON.stringify({
          name: endpoint.name,
          host: endpoint.host,
          port: endpoint.port,
          username: endpoint.username,
          tls_mode: endpoint.tls_mode,
          max_connections: endpoint.max_connections,
          priority: endpoint.priority,
          weight: endpoint.weight,
          status: endpoint.status,
          ca_cert_pem: endpoint.ca_cert_pem || undefined,
          client_cert_pem: endpoint.client_cert_pem || undefined,
          is_active: isActive,
        }),
      })
      if (!resp.ok) throw new Error(await readAPIMessage(resp, 'Cannot update status.'))
      toast.success(`Endpoint status updated successfully!`)
      setRefreshKey((v) => v + 1)
    } catch (err) {
      console.error(err)
      toast.error(err instanceof Error ? err.message : 'Cannot update status.')
    }
  }

  // Lọc dữ liệu client-side tương tự Zone page
  const filteredItems = useMemo(() => {
    const items = state.data?.items ?? []
    const normalizedQuery = query.trim().toLowerCase()
    if (!normalizedQuery) return items

    return items.filter((item) =>
      [item.name, item.host, item.provider, item.tls_mode, item.status].some((value) =>
        String(value || '').toLowerCase().includes(normalizedQuery)
      )
    )
  }, [query, state.data])

  // Phân trang client-side tương tự Zone page
  const totalPages = Math.max(1, Math.ceil(filteredItems.length / pageSize))
  const safePage = Math.min(currentPage, totalPages)
  const startIndex = filteredItems.length === 0 ? 0 : (safePage - 1) * pageSize
  const endIndex = Math.min(startIndex + pageSize, filteredItems.length)
  const visibleItems = filteredItems.slice(startIndex, endIndex)

  const goToPage = (page: number) => {
    setCurrentPage(Math.min(Math.max(page, 1), totalPages))
  }

  return (
    <div className="space-y-6">
      {/* Tiêu đề theo style Zone Management */}
      <div className="space-y-1">
        <h2 className="text-2xl font-semibold tracking-[-0.02em] text-foreground">
          Mail Endpoints
        </h2>
        <p className="text-sm text-muted-foreground">
          Manage physical mail delivery providers, SMTP configurations, and credential status.
        </p>
      </div>

      {/* Thanh công cụ: Tìm kiếm + Refresh bên trái, Add Endpoint bên phải */}
      <div className="mb-6 flex flex-col gap-4 md:flex-row md:items-center md:justify-between">
        <div className="flex w-full flex-col gap-3 sm:flex-row md:max-w-140 md:justify-start flex-1">
          {/* Ô nhập từ khóa tìm kiếm */}
          <div className="relative flex-1 max-w-96">
            <Search className="pointer-events-none absolute left-4 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(event) => {
                setQuery(event.target.value)
                setCurrentPage(1)
              }}
              placeholder="Search endpoints..."
              className="h-12 rounded-lg border-border bg-background pl-11 text-sm shadow-none"
            />
          </div>
          {/* Nút trigger tải lại dữ liệu thủ công */}
          <Button
            type="button"
            variant="outline"
            onClick={() => setRefreshKey((value) => value + 1)}
            disabled={state.loading}
            className="h-12 rounded-lg px-4 text-sm font-semibold cursor-pointer shrink-0"
          >
            <RefreshCcw className={cn('size-4 mr-2', state.loading && 'animate-spin')} />
            Refresh
          </Button>
        </div>

        {canWrite ? (
          <Button asChild className="h-12 rounded-lg px-6 text-sm font-semibold shadow-sm shrink-0 cursor-pointer">
            <Link to="/mail/endpoints/new">
              <Plus className="size-4 mr-2" />
              Add Endpoint
            </Link>
          </Button>
        ) : (
          <Button
            disabled
            className="h-12 rounded-lg px-6 text-sm font-semibold shadow-sm shrink-0 opacity-50 cursor-not-allowed"
            title="Vui lòng chọn một Zone cụ thể để thêm mới Endpoint"
          >
            <Plus className="size-4 mr-2" />
            Add Endpoint
          </Button>
        )}
      </div>



      {/* Bảng Dữ liệu chính */}
      <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7">
        <Table>
          <TableHeader>
            <TableRow className="border-border/80 hover:bg-transparent">
              <TableHead className="w-28 pb-4 text-sm font-medium text-muted-foreground">Status</TableHead>
              <TableHead className="pb-4 text-sm font-medium text-muted-foreground">Name</TableHead>
              <TableHead className="pb-4 text-sm font-medium text-muted-foreground">Host</TableHead>
              <TableHead className="pb-4 text-sm font-medium text-muted-foreground">TLS</TableHead>
              <TableHead className="pb-4 text-sm font-medium text-muted-foreground">Capacity</TableHead>
              <TableHead className="pb-4 text-sm font-medium text-muted-foreground">Secret</TableHead>
              <TableHead className="text-right pb-4 text-sm font-medium text-muted-foreground w-48">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {/* Loading state */}
            {state.loading &&
              Array.from({ length: pageSize }).map((_, index) => (
                <TableRow key={index} className="border-border/80 hover:bg-transparent">
                  {Array.from({ length: 7 }).map((__, cellIndex) => (
                    <TableCell key={cellIndex} className="py-4">
                      <Skeleton className="h-5 w-full" />
                    </TableCell>
                  ))}
                </TableRow>
              ))}

            {/* Empty state */}
            {!state.loading && filteredItems.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={7} className="h-40 text-center">
                  <div className="flex flex-col items-center justify-center gap-2 text-muted-foreground">
                    <Search className="size-6 opacity-60" />
                    <p className="text-sm font-semibold text-foreground">No endpoints found</p>
                    <p className="text-sm">
                      {query.trim() ? 'Try another endpoint name, host or provider.' : 'Create an endpoint to start mail delivery.'}
                    </p>
                  </div>
                </TableCell>
              </TableRow>
            )}

            {/* Render items */}
            {!state.loading &&
              visibleItems.map((item) => (
                <TableRow key={item.id} className="border-border/80 hover:bg-muted/30">
                  <TableCell className="py-3.5">
                    <StatusBadge status={item.status || 'disabled'} />
                  </TableCell>
                  <TableCell className="py-3.5 text-sm font-medium text-foreground">
                    <Link to="/mail/endpoints/$id/edit" params={{ id: item.id }} className="text-sm font-semibold text-primary hover:underline">
                      {item.name}
                    </Link>
                  </TableCell>
                  <TableCell className="py-3.5 text-sm text-muted-foreground">
                    {item.host}:{item.port}
                  </TableCell>
                  <TableCell className="py-3.5 text-sm text-muted-foreground">
                    {item.tls_mode ? item.tls_mode.split('_').map(p => p.charAt(0).toUpperCase() + p.slice(1)).join(' ') : 'None'}
                  </TableCell>
                  <TableCell className="py-3.5 text-sm text-muted-foreground">
                    {item.max_connections} conn
                  </TableCell>
                  <TableCell className="py-3.5 text-sm text-muted-foreground">
                    {item.has_secret ? (
                      <Badge variant="secondary" className="bg-emerald-500/10 text-emerald-700 border-none dark:bg-emerald-500/20 dark:text-emerald-400">Configured</Badge>
                    ) : (
                      <Badge variant="secondary" className="bg-amber-500/10 text-amber-700 border-none dark:bg-amber-500/20 dark:text-amber-400">Missing</Badge>
                    )}
                  </TableCell>
                  <TableCell className="py-3.5 text-right">
                    <div className="flex justify-end items-center gap-2">
                      {canWrite ? (
                        <Button asChild variant="outline" size="sm" className="h-9 cursor-pointer">
                          <Link to="/mail/endpoints/$id/edit" params={{ id: item.id }}>Edit</Link>
                        </Button>
                      ) : (
                        <Button disabled variant="outline" size="sm" className="h-9 opacity-50 cursor-not-allowed" title="Vui lòng chọn Zone để chỉnh sửa">
                          Edit
                        </Button>
                      )}
                      
                      {item.is_active ? (
                        <Button
                          variant="outline"
                          size="icon-sm"
                          disabled={!canWrite}
                          onClick={() => void updateStatus(item, false)}
                          title={canWrite ? "Suspend" : "Vui lòng chọn Zone để tạm dừng"}
                          className={cn("h-9 w-9", canWrite ? "cursor-pointer" : "opacity-50 cursor-not-allowed")}
                        >
                          <Pause className="size-4 text-amber-500 fill-amber-500/20" />
                        </Button>
                      ) : (
                        <Button
                          variant="outline"
                          size="icon-sm"
                          disabled={!canWrite}
                          onClick={() => void updateStatus(item, true)}
                          title={canWrite ? "Activate" : "Vui lòng chọn Zone để kích hoạt"}
                          className={cn("h-9 w-9", canWrite ? "cursor-pointer" : "opacity-50 cursor-not-allowed")}
                        >
                          <Play className="size-4 text-emerald-500 fill-emerald-500/20" />
                        </Button>
                      )}

                      <Button variant="outline" size="sm" onClick={() => void tryConnect(item)} className="h-9 cursor-pointer">
                        Try
                      </Button>

                      <Button
                        variant="destructive"
                        size="icon-sm"
                        disabled={!canWrite}
                        onClick={() => void deleteEndpoint(item)}
                        aria-label={`Delete ${item.name}`}
                        title={canWrite ? "Delete" : "Vui lòng chọn Zone để xóa"}
                        className={cn("h-9 w-9", canWrite ? "cursor-pointer" : "opacity-50 cursor-not-allowed")}
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
          </TableBody>
        </Table>

        {/* Footer phân trang style ZoneTable */}
        {!state.loading && filteredItems.length > 0 && (
          <div className="mt-5 flex flex-col gap-3 text-sm text-muted-foreground md:flex-row md:items-center md:justify-between">
            <p>
              Showing {startIndex + 1} to {endIndex} of {filteredItems.length} endpoints
            </p>

            <div className="flex items-center gap-3">
              <Button
                type="button"
                variant="outline"
                size="icon"
                disabled={safePage <= 1 || state.loading}
                onClick={() => goToPage(safePage - 1)}
                className="size-9 rounded-lg text-muted-foreground"
              >
                <ChevronLeft className="size-4" />
              </Button>

              {Array.from({ length: totalPages }).map((_, index) => {
                const page = index + 1
                return (
                  <Button
                    key={page}
                    type="button"
                    variant="outline"
                    size="icon"
                    disabled={state.loading}
                    onClick={() => goToPage(page)}
                    className={cn(
                      'size-9 rounded-lg',
                      page === safePage ? 'border-primary text-primary' : 'text-muted-foreground'
                    )}
                  >
                    {page}
                  </Button>
                )
              })}

              <Button
                type="button"
                variant="outline"
                size="icon"
                disabled={safePage >= totalPages || state.loading}
                onClick={() => goToPage(safePage + 1)}
                className="size-9 rounded-lg text-muted-foreground"
              >
                <ChevronRight className="size-4" />
              </Button>

              <div className="relative ml-4">
                <select
                  value={pageSize}
                  onChange={(event) => {
                    setPageSize(Number(event.target.value))
                    setCurrentPage(1)
                  }}
                  className="h-9 appearance-none rounded-lg border border-border bg-card py-0 pl-4 pr-9 text-sm font-medium text-muted-foreground outline-none transition-colors hover:bg-muted/50 focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
                >
                  {[4, 8, 12].map((size) => (
                    <option key={size} value={size}>
                      {size} / page
                    </option>
                  ))}
                </select>
                <ChevronDown className="pointer-events-none absolute right-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              </div>
            </div>
          </div>
        )}
      </div>

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
