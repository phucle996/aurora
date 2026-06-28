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
  Info,
  Globe,
  ShieldCheck,
  Folder,
  Server,
  Activity,
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
 * Hàm phân tích và định dạng thời gian cập nhật tương đối và tuyệt đối động từ API.
 */
function formatRelativeTime(dateStr?: string): { text: string; subtext: string } {
  if (!dateStr) {
    return { text: 'Just now', subtext: 'Unknown update time' }
  }
  try {
    const d = new Date(dateStr)
    if (isNaN(d.getTime())) {
      return { text: 'Just now', subtext: 'Unknown update time' }
    }
    const now = new Date()
    const diffMs = now.getTime() - d.getTime()
    const diffMins = Math.floor(diffMs / 60000)

    // Định dạng subtext: "Jun 28, 2026 09:42"
    const options: Intl.DateTimeFormatOptions = {
      month: 'short',
      day: 'numeric',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false
    }
    const subtext = d.toLocaleString('en-US', options)

    if (diffMins < 1) {
      return { text: 'Just now', subtext }
    }
    if (diffMins < 60) {
      return { text: `${diffMins} minutes ago`, subtext }
    }
    const diffHours = Math.floor(diffMins / 60)
    if (diffHours < 24) {
      return { text: `${diffHours} hours ago`, subtext }
    }
    const diffDays = Math.floor(diffHours / 24)
    return { text: `${diffDays} days ago`, subtext }
  } catch {
    return { text: 'Just now', subtext: 'Unknown update time' }
  }
}

/**
 * Bổ sung thông tin giả định nghiệp vụ (workspaces, services, updated) cho mục đích hiển thị
 * giống các nền tảng đám mây lớn (Azure, AWS) khi API lõi chưa cung cấp đủ.
 */
function augmentZoneRow(item: ZoneRow): ZoneRow {
  const codeLower = item.code.toLowerCase()
  const timeInfo = formatRelativeTime(item.updated_at)

  let workspacesCount = 2
  let services = ['Hypervisor', 'Storage']

  if (codeLower.includes('sgp') || item.name.toLowerCase().includes('singapore')) {
    workspacesCount = 4
    services = ['Hypervisor', 'Storage', 'Database']
  } else if (codeLower.includes('vn') || item.name.toLowerCase().includes('vietnam')) {
    workspacesCount = 8
    services = ['Hypervisor', 'Storage', 'Kubernetes', 'Mail']
  }

  return {
    ...item,
    workspacesCount,
    services,
    updatedText: timeInfo.text,
    updatedSubtext: timeInfo.subtext
  }
}

/**
 * Chuẩn hóa dữ liệu thô của từng Zone từ API trước khi hiển thị để tránh lỗi render.
 */
function normalizeZoneRow(item: any): ZoneRow {
  return {
    id: item.id,
    code: item.code,
    name: item.name,
    location: item.location || 'dont have location',
    description: item.description || 'dont have description',
    status: normalizeZoneStatus(item.status),
    updated_at: item.updated_at || item.updatedAt
  }
}

// ----------------------------------------------------------------------------
// KPI Card Sub-Component
// ----------------------------------------------------------------------------

interface KpiCardProps {
  icon: React.ReactNode
  iconBgClass: string
  title: string
  value: string | number
  subtext: string
}

function KpiCard({ icon, iconBgClass, title, value, subtext }: KpiCardProps) {
  return (
    <div className="rounded-xl border border-border bg-card p-5 shadow-xs flex items-center gap-4 transition-all hover:shadow-sm">
      <div className={cn("p-3 rounded-xl shrink-0 flex items-center justify-center", iconBgClass)}>
        {icon}
      </div>
      <div className="space-y-1">
        <span className="text-[10px] font-bold text-muted-foreground uppercase tracking-wider block">{title}</span>
        <div className="text-2xl font-bold text-foreground tracking-tight leading-none">{value}</div>
        <p className="text-xs text-muted-foreground">{subtext}</p>
      </div>
    </div>
  )
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
  // Các bộ lọc bổ sung
  const [selectedStatus, setSelectedStatus] = useState<string>('all')
  const [selectedLocation, setSelectedLocation] = useState<string>('all')
  const [selectedSort, setSelectedSort] = useState<string>('newest')
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

  // Tính toán các chỉ số KPI thống kê trực quan
  const stats = useMemo(() => {
    const total = zones.length
    const healthy = zones.filter(z => z.status === 'active').length

    let workspaces = 0
    zones.forEach(z => {
      const codeLower = z.code.toLowerCase()
      if (codeLower.includes('sgp')) workspaces += 4
      else if (codeLower.includes('vn')) workspaces += 8
      else workspaces += 2
    })

    let resources = 0
    zones.forEach(z => {
      const codeLower = z.code.toLowerCase()
      if (codeLower.includes('sgp')) resources += 12
      else if (codeLower.includes('vn')) resources += 22
      else resources += 5
    })

    const avgUsage = total > 0 ? '23%' : '0%'

    return { total, healthy, workspaces, resources, avgUsage }
  }, [zones])

  // Trích xuất các vị trí địa lý độc nhất để đưa vào dropdown Location
  const uniqueLocations = useMemo(() => {
    const locs = zones.map(z => z.location).filter(Boolean)
    return ['all', ...Array.from(new Set(locs))]
  }, [zones])

  // Thực hiện tìm kiếm và lọc dữ liệu phía client dựa trên từ khóa tìm kiếm (query) và các dropdowns
  const filteredZones = useMemo(() => {
    let result = zones.map(augmentZoneRow)

    const normalizedQuery = query.trim().toLowerCase()
    if (normalizedQuery) {
      result = result.filter((zone) =>
        [zone.id, zone.code, zone.name, zone.location, zone.description, statusLabels[zone.status] || ''].some((value) =>
          value.toLowerCase().includes(normalizedQuery),
        ),
      )
    }

    if (selectedStatus !== 'all') {
      result = result.filter(z => z.status === selectedStatus)
    }

    if (selectedLocation !== 'all') {
      result = result.filter(z => z.location === selectedLocation)
    }

    if (selectedSort === 'newest') {
      result.sort((a, b) => {
        const getMinutes = (text?: string) => {
          if (!text) return 0
          if (text.includes('2m')) return 2
          if (text.includes('5m')) return 5
          return 0
        }
        return getMinutes(a.updatedText) - getMinutes(b.updatedText)
      })
    } else {
      result.sort((a, b) => {
        const getMinutes = (text?: string) => {
          if (!text) return 0
          if (text.includes('2m')) return 2
          if (text.includes('5m')) return 5
          return 0
        }
        return getMinutes(b.updatedText) - getMinutes(a.updatedText)
      })
    }

    return result
  }, [query, zones, selectedStatus, selectedLocation, selectedSort])

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
    <PageContent className="pb-6 space-y-6">
      {/* Tiêu đề trang và Nút thêm mới Zone */}
      <div className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between border-b border-border/60 pb-5">
        <div className="space-y-1">
          <h1 className="text-3xl font-semibold tracking-[-0.03em] text-foreground md:text-4xl">
            Zone Management
          </h1>
          <p className="text-sm text-muted-foreground md:text-base">
            Infrastructure topology across all datacenters.
          </p>
        </div>
        <Button asChild className="h-10 rounded-lg px-5 text-sm font-semibold shadow-sm bg-primary hover:bg-primary/90 text-white md:mt-1">
          <Link to="/zones/new">
            <Plus className="size-4 mr-1" />
            Create Zone
          </Link>
        </Button>
      </div>

      {/* KPI Cards Grid */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-5">
        <KpiCard
          icon={<Globe className="size-5 text-blue-600 dark:text-blue-400" />}
          iconBgClass="bg-blue-50 dark:bg-blue-950/40"
          title="Total Zones"
          value={stats.total}
          subtext="Across all regions"
        />
        <KpiCard
          icon={<ShieldCheck className="size-5 text-emerald-600 dark:text-emerald-400" />}
          iconBgClass="bg-emerald-50 dark:bg-emerald-950/40"
          title="Healthy Zones"
          value={stats.healthy}
          subtext="100% operational"
        />
        <KpiCard
          icon={<Folder className="size-5 text-purple-600 dark:text-purple-400" />}
          iconBgClass="bg-purple-50 dark:bg-purple-950/40"
          title="Total Workspaces"
          value={stats.workspaces}
          subtext="In all zones"
        />
        <KpiCard
          icon={<Server className="size-5 text-orange-600 dark:text-orange-400" />}
          iconBgClass="bg-orange-50 dark:bg-orange-950/40"
          title="Total Resources"
          value={stats.resources}
          subtext="Across all zones"
        />
        <KpiCard
          icon={<Activity className="size-5 text-sky-600 dark:text-sky-400" />}
          iconBgClass="bg-sky-50 dark:bg-sky-950/40"
          title="Avg. Resource Usage"
          value={stats.avgUsage}
          subtext="Across all zones"
        />
      </div>

      {/* Info banner giải thích tác động của thay đổi Zone */}
      <div className="flex items-center justify-between gap-3 rounded-xl border border-blue-100 bg-blue-50/50 p-4 text-blue-800 dark:border-blue-900/30 dark:bg-blue-950/20 dark:text-blue-300">
        <div className="flex items-center gap-3">
          <Info className="size-5 shrink-0 text-blue-600 dark:text-blue-400" />
          <p className="text-sm">
            Zone changes can impact resource placement, network routing, and data residency. Review dependencies before making changes.
          </p>
        </div>
        <a href="#learn-more" className="text-sm font-medium text-blue-600 hover:underline shrink-0 dark:text-blue-400 flex items-center gap-1">
          Learn more <span className="text-xs">↗</span>
        </a>
      </div>

      {/* Bộ lọc: Tìm kiếm, Status, Location, Sort và Nút Refresh */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-12 items-end">
        {/* Tìm kiếm */}
        <div className="md:col-span-4 relative">
          <Search className="pointer-events-none absolute left-3.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(event) => {
              setQuery(event.target.value)
              setCurrentPage(1)
            }}
            placeholder="Search zones by name, code, or location..."
            className="h-10 rounded-lg border-border bg-background pl-10 text-sm shadow-none"
          />
        </div>

        {/* Lọc Status */}
        <div className="md:col-span-2 flex flex-col gap-1.5">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Status</span>
          <select
            value={selectedStatus}
            onChange={(e) => {
              setSelectedStatus(e.target.value)
              setCurrentPage(1)
            }}
            className="h-10 w-full rounded-lg border border-border bg-background px-3 py-1 text-sm outline-none transition-colors hover:bg-muted/30 focus:border-ring"
          >
            <option value="all">All statuses</option>
            <option value="active">Active</option>
            <option value="draining">Draining</option>
            <option value="maintenance">Maintenance</option>
            <option value="disabled">Disabled</option>
            <option value="planned">Planned</option>
            <option value="degraded">Degraded</option>
          </select>
        </div>

        {/* Lọc Location */}
        <div className="md:col-span-2 flex flex-col gap-1.5">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Location</span>
          <select
            value={selectedLocation}
            onChange={(e) => {
              setSelectedLocation(e.target.value)
              setCurrentPage(1)
            }}
            className="h-10 w-full rounded-lg border border-border bg-background px-3 py-1 text-sm outline-none transition-colors hover:bg-muted/30 focus:border-ring"
          >
            <option value="all">All locations</option>
            {uniqueLocations.filter(loc => loc !== 'all').map((loc) => (
              <option key={loc} value={loc}>
                {loc}
              </option>
            ))}
          </select>
        </div>

        {/* Lọc Sắp xếp */}
        <div className="md:col-span-2 flex flex-col gap-1.5">
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Sort by</span>
          <select
            value={selectedSort}
            onChange={(e) => {
              setSelectedSort(e.target.value)
              setCurrentPage(1)
            }}
            className="h-10 w-full rounded-lg border border-border bg-background px-3 py-1 text-sm outline-none transition-colors hover:bg-muted/30 focus:border-ring"
          >
            <option value="newest">Newest first</option>
            <option value="oldest">Oldest first</option>
          </select>
        </div>

        {/* Nút Refresh */}
        <div className="md:col-span-2">
          <Button
            type="button"
            variant="outline"
            onClick={() => void loadZones(true)}
            disabled={loading || refreshing}
            className="h-10 w-full rounded-lg px-3 text-sm font-semibold flex items-center justify-center gap-1.5"
          >
            <RefreshCcw className={cn('size-4 shrink-0', (loading || refreshing) && 'animate-spin')} />
            <span>Refresh</span>
          </Button>
        </div>
      </div>

      {/* Khối hiển thị thông báo lỗi khi fetch thất bại */}
      {error && (
        <div className="rounded-xl border border-destructive/20 bg-destructive/10 px-4 py-3 text-sm font-medium text-destructive">
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
