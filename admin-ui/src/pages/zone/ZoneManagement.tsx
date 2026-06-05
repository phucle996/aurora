/**
 * ZoneManagement.tsx — Trang quản lý và giám sát hạ tầng các Zone.
 *
 * File này đóng vai trò là Page Component điều phối danh sách các Zone hạ tầng.
 * Thực hiện truy vấn API, hỗ trợ tìm kiếm client-side, phân trang dữ liệu, và
 * cung cấp liên kết để thêm mới Zone.
 *
 * Import pattern cho consumer:
 *   import ZoneManagementPage from '@/pages/zone/ZoneManagement'
 */

import { useEffect, useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'

import {
  Plus,
  RefreshCcw,
  Search,
  TriangleAlert,
} from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Fetch } from '@/lib/fetch'
import { cn } from '@/lib/utils'
import { PageContent } from '@/components/layout/layout'

import ZoneTable, { type ZoneRow, type ZoneStatus } from './sections/ZoneTable'

// ----------------------------------------------------------------------------
// Types & Helpers
// ----------------------------------------------------------------------------

const statusLabels: Record<ZoneStatus, string> = {
  planned: 'Planned',
  active: 'Active',
  degraded: 'Degraded',
  maintenance: 'Maintenance',
  disabled: 'Disabled',
  draining: 'Draining',
}

type ZoneListResponse = {
  message?: string
  error?: string
  data?: {
    items?: ZoneRow[]
    total?: number
  }
}

/**
 * Chuẩn hóa giá trị string từ API thành kiểu dữ liệu ZoneStatus hợp lệ.
 */
function normalizeZoneStatus(value: string): ZoneStatus {
  switch (value) {
    case 'active':
    case 'draining':
    case 'maintenance':
    case 'disabled':
    case 'planned':
    case 'degraded':
      return value
    default:
      return 'active'
  }
}

/**
 * Trích xuất thông tin lỗi từ response body của API khi request thất bại.
 */
async function readErrorMessage(response: Response) {
  try {
    const payload = (await response.json()) as ZoneListResponse
    return payload.message || payload.error || 'Cannot load zones'
  } catch {
    return 'Cannot load zones'
  }
}

/**
 * Chuẩn hóa dữ liệu thô của từng Zone từ API trước khi hiển thị để tránh lỗi render.
 */
function normalizeZoneRow(item: ZoneRow): ZoneRow {
  return {
    id: item.id,
    code: item.code || item.id,
    name: item.name || item.code || item.id,
    location: item.location || '—',
    description: item.description || 'No description provided.',
    status: normalizeZoneStatus(item.status),
    created_at: item.created_at,
    updated_at: item.updated_at,
  }
}

// ----------------------------------------------------------------------------
// Network Optimization (Deduplication)
// ----------------------------------------------------------------------------

/**
 * Global Promise Deduplicator nhằm ngăn chặn việc gọi trùng lặp request API
 * khi React 18 chạy Strict Mode (gây ra tình trạng double-mount khi khởi tạo component).
 */
let activeZonesPromise: Promise<ZoneListResponse> | null = null

// ----------------------------------------------------------------------------
// Page Component
// ----------------------------------------------------------------------------

/**
 * Component trang quản lý Zone, chứa logic fetch dữ liệu, lọc dữ liệu và điều phối phân trang.
 */
export default function ZoneManagementPage() {
  // Quản lý trạng thái danh sách Zone gốc nhận từ API
  const [zones, setZones] = useState<ZoneRow[]>([])
  // Trạng thái hiển thị loading spinner cho đợt tải dữ liệu đầu tiên
  const [loading, setLoading] = useState(true)
  // Trạng thái hiển thị hiệu ứng xoay nút refresh khi cập nhật âm thầm (silent refresh)
  const [refreshing, setRefreshing] = useState(false)
  // Lưu trữ chuỗi thông báo lỗi khi API trả về lỗi
  const [error, setError] = useState('')
  // Chuỗi tìm kiếm do người dùng nhập vào
  const [query, setQuery] = useState('')
  // Số lượng hàng dữ liệu tối đa hiển thị trên mỗi trang
  const [pageSize, setPageSize] = useState(8)
  // Chỉ mục trang hiện tại mà người dùng đang xem
  const [currentPage, setCurrentPage] = useState(1)

  /**
   * Gọi API tải thông tin danh sách Zone.
   *
   * @param isSilent Nếu true, ứng dụng sẽ xoay nút Refresh thay vì hiển thị skeleton loading toàn trang.
   */
  async function loadZones(isSilent = false) {
    if (isSilent) {
      setRefreshing(true)
    } else {
      setLoading(true)
    }
    setError('')
    try {
      // Cơ chế chống trùng lặp request: Nếu có request đang chạy, tái sử dụng Promise cũ
      if (!activeZonesPromise) {
        activeZonesPromise = Fetch('/admin/core/zones').then(async (response) => {
          if (!response.ok) {
            const errText = await readErrorMessage(response)
            throw new Error(errText)
          }
          return response.json() as Promise<ZoneListResponse>
        })
      }

      const payload = await activeZonesPromise
      setZones((payload.data?.items ?? []).map(normalizeZoneRow))
      // Đưa trạng thái phân trang về trang 1 nếu không phải là cập nhật âm thầm
      if (!isSilent) {
        setCurrentPage(1)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot load zones')
      if (!isSilent) {
        setZones([])
      }
    } finally {
      // Hủy vết của Promise sau khi hoàn tất để chuẩn bị cho lần fetch kế tiếp
      activeZonesPromise = null
      setLoading(false)
      setRefreshing(false)
    }
  }

  // Tự động tải dữ liệu khi component được mount lần đầu
  useEffect(() => {
    const timer = setTimeout(() => {
      void loadZones()
    }, 0)
    return () => clearTimeout(timer)
  }, [])

  // Thực hiện tìm kiếm và lọc dữ liệu phía client dựa trên từ khóa tìm kiếm (query)
  const filteredZones = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    if (!normalizedQuery) return zones

    return zones.filter((zone) =>
      [zone.id, zone.code, zone.name, zone.location, zone.description, statusLabels[zone.status]].some((value) =>
        value.toLowerCase().includes(normalizedQuery),
      ),
    )
  }, [query, zones])

  // Tính toán các thông số bổ trợ phục vụ phân trang
  const totalPages = Math.max(1, Math.ceil(filteredZones.length / pageSize))
  const safePage = Math.min(currentPage, totalPages)
  const startIndex = filteredZones.length === 0 ? 0 : (safePage - 1) * pageSize
  const endIndex = Math.min(startIndex + pageSize, filteredZones.length)
  const visibleZones = filteredZones.slice(startIndex, endIndex)

  // Hàm chuyển đổi trang an toàn
  const goToPage = (page: number) => {
    setCurrentPage(Math.min(Math.max(page, 1), totalPages))
  }

  return (
    <PageContent className="pb-0">
      {/* Tiêu đề trang và Nút thêm mới Zone */}
      <div className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between">
        <div className="space-y-2">
          <h1 className="text-3xl font-semibold tracking-[-0.03em] text-foreground md:text-4xl">
            Zone Management
          </h1>
          <p className="text-sm text-muted-foreground md:text-base">
            Manage infrastructure zones across the platform.
          </p>
        </div>
        <Button asChild className="h-12 rounded-lg px-6 text-sm font-semibold shadow-sm md:mt-1">
          <Link to="/zones/new">
            <Plus className="size-4" />
            Add Zone
          </Link>
        </Button>
      </div>

      {/* Warning banner lưu ý Zone là thực thể topology cao nhất */}
      <div className="flex items-start gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 dark:border-amber-500/20 dark:bg-amber-500/10">
        <TriangleAlert className="mt-px size-4 shrink-0 text-amber-600 dark:text-amber-400" />
        <p className="text-sm leading-relaxed text-amber-800 dark:text-amber-300">
          <span className="font-semibold">Zone là root topology logic.</span>{' '}
          Mọi thay đổi trên zone ảnh hưởng trực tiếp đến toàn bộ hạ tầng
          dataplane. Kiểm tra kỹ trước khi thực hiện bất kỳ hành động nào.
        </p>
      </div>

      {/* Thanh công cụ: Tìm kiếm và Nút Refresh tải lại dữ liệu */}
      <div className="mb-6 flex flex-col gap-4 md:flex-row md:items-center md:justify-end">
        <div className="flex w-full flex-col gap-3 sm:flex-row md:max-w-140 md:justify-end">
          {/* Ô nhập từ khóa tìm kiếm */}
          <div className="relative flex-1">
            <Search className="pointer-events-none absolute left-4 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(event) => {
                setQuery(event.target.value)
                setCurrentPage(1) // Reset về trang 1 khi lọc danh sách
              }}
              placeholder="Search zones..."
              className="h-12 rounded-lg border-border bg-background pl-11 text-sm shadow-none"
            />
          </div>
          {/* Nút trigger tải lại dữ liệu thủ công */}
          <Button
            type="button"
            variant="outline"
            onClick={() => void loadZones(true)}
            disabled={loading || refreshing}
            className="h-12 rounded-lg px-4 text-sm font-semibold"
          >
            <RefreshCcw className={cn('size-4', (loading || refreshing) && 'animate-spin')} />
            Refresh
          </Button>
        </div>
      </div>

      {/* Khối hiển thị thông báo lỗi khi fetch thất bại */}
      {error && (
        <div className="mb-5 rounded-xl border border-destructive/20 bg-destructive/10 px-4 py-3 text-sm font-medium text-destructive">
          Cannot load zones: {error}
        </div>
      )}

      {/* Component bảng dữ liệu thực hiện render danh sách các Zone đã được lọc */}
      <ZoneTable
        loading={loading}
        refreshing={refreshing}
        zones={visibleZones}
        query={query}
        pageSize={pageSize}
        currentPage={currentPage}
        totalPages={totalPages}
        startIndex={startIndex}
        endIndex={endIndex}
        totalZones={filteredZones.length}
        goToPage={goToPage}
        setPageSize={setPageSize}
        setCurrentPage={setCurrentPage}
      />
    </PageContent>
  )
}
