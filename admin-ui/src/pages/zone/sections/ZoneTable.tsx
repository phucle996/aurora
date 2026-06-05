/**
 * ZoneTable.tsx — Component hiển thị danh sách và quản lý phân trang cho các Zone.
 *
 * File này chứa component hiển thị bảng cấu hình hạ tầng mạng lưới Zone.
 * Hỗ trợ các trạng thái loading skeleton, empty state, pagination, và thay đổi page size.
 *
 * Import pattern cho consumer:
 *   import ZoneTable from '@/pages/zone/sections/ZoneTable'
 *   import type { ZoneRow, ZoneStatus } from '@/pages/zone/sections/ZoneTable'
 */

import { Link } from '@tanstack/react-router'
import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Search,
} from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { cn } from '@/lib/utils'

// ----------------------------------------------------------------------------
// Types & Interfaces
// ----------------------------------------------------------------------------

/**
 * Các trạng thái hoạt động hợp lệ của một Zone trong hệ thống.
 */
export type ZoneStatus = 'planned' | 'active' | 'degraded' | 'maintenance' | 'disabled' | 'draining'

/**
 * Cấu trúc dữ liệu của một hàng biểu diễn thông tin Zone trên Table.
 */
export type ZoneRow = {
  id: string
  code: string
  name: string
  location: string
  description: string
  status: ZoneStatus
}

const statusLabels: Record<ZoneStatus, string> = {
  planned: 'Planned',
  active: 'Active',
  degraded: 'Degraded',
  maintenance: 'Maintenance',
  disabled: 'Disabled',
  draining: 'Draining',
}

// ----------------------------------------------------------------------------
// Helper Components
// ----------------------------------------------------------------------------

/**
 * Component Badge nhỏ biểu diễn trạng thái hoạt động trực quan cho từng Zone.
 */
export function StatusBadge({ status }: { status: ZoneStatus }) {
  return (
    <Badge
      variant="outline"
      className={cn(
        'h-8 rounded-lg border px-3 text-sm font-medium',
        status === 'active' && 'border-emerald-200 bg-emerald-50 text-emerald-700',
        status === 'draining' && 'border-amber-200 bg-amber-50 text-amber-700',
        status === 'maintenance' && 'border-violet-200 bg-violet-50 text-violet-700',
        status === 'disabled' && 'border-slate-200 bg-slate-50 text-slate-600',
        status === 'planned' && 'border-sky-200 bg-sky-50 text-sky-700',
        status === 'degraded' && 'border-red-200 bg-red-50 text-red-700',
      )}
    >
      {statusLabels[status] ?? status}
    </Badge>
  )
}

/**
 * Thuộc tính đầu vào của component ZoneTable.
 */
interface ZoneTableProps {
  loading: boolean
  refreshing: boolean
  zones: ZoneRow[]
  query: string
  pageSize: number
  currentPage: number
  totalPages: number
  startIndex: number
  endIndex: number
  totalZones: number
  goToPage: (page: number) => void
  setPageSize: (size: number) => void
  setCurrentPage: (page: number) => void
}

// ----------------------------------------------------------------------------
// Main Component
// ----------------------------------------------------------------------------

/**
 * Bảng dữ liệu hiển thị danh sách các Zone và điều khiển phân trang.
 *
 * Component hỗ trợ:
 * - Render hiệu ứng skeleton khi loading dữ liệu.
 * - Hiển thị empty state khi không tìm thấy kết quả phù hợp.
 * - Tích hợp điều hướng phân trang và tùy chỉnh kích thước page size.
 */
export default function ZoneTable({
  loading,
  refreshing,
  zones,
  query,
  pageSize,
  currentPage,
  totalPages,
  startIndex,
  endIndex,
  totalZones,
  goToPage,
  setPageSize,
  setCurrentPage,
}: ZoneTableProps) {
  // Phòng ngừa trường hợp currentPage vượt quá totalPages thực tế khi danh sách bị lọc
  const safePage = Math.min(currentPage, totalPages)

  return (
    <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7">
      {/* Khởi tạo cấu trúc bảng dữ liệu hiển thị Zone */}
      <Table>
        <TableHeader>
          <TableRow className="border-border/80 hover:bg-transparent">
            <TableHead className="w-32 pb-4 text-sm font-medium text-muted-foreground">
              Status
            </TableHead>
            <TableHead className="pb-4 text-sm font-medium text-muted-foreground">
              Name
            </TableHead>
            <TableHead className="pb-4 text-sm font-medium text-muted-foreground">
              Code
            </TableHead>
            <TableHead className="min-w-[200px] pb-4 text-sm font-medium text-muted-foreground">
              Location
            </TableHead>
            <TableHead className="min-w-[500px] pb-4 text-sm font-medium text-muted-foreground">
              Description
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {/* Trạng thái 1: Đang tải dữ liệu - Render các dòng loading skeleton */}
          {loading &&
            Array.from({ length: pageSize }).map((_, index) => (
              <TableRow key={index} className="border-border/80 hover:bg-transparent">
                {Array.from({ length: 5 }).map((__, cellIndex) => (
                  <TableCell key={cellIndex} className="py-4">
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                ))}
              </TableRow>
            ))}

          {/* Trạng thái 2: Không có dữ liệu - Render giao diện empty state */}
          {!loading && zones.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={5} className="h-40 text-center">
                <div className="flex flex-col items-center justify-center gap-2 text-muted-foreground">
                  <Search className="size-6 opacity-60" />
                  <p className="text-sm font-semibold text-foreground">No zones found</p>
                  <p className="text-sm">
                    {query.trim() ? 'Try another zone name, code, location, or status.' : 'Create a zone to start topology management.'}
                  </p>
                </div>
              </TableCell>
            </TableRow>
          )}

          {/* Trạng thái 3: Đã có dữ liệu - Duyệt và render danh sách các hàng dữ liệu Zone */}
          {!loading && zones.map((zone) => (
            <TableRow key={zone.id} className="border-border/80 hover:bg-muted/30">
              {/* Cột 1: Trạng thái hoạt động hiển thị dưới dạng StatusBadge */}
              <TableCell className="py-3.5">
                <StatusBadge status={zone.status} />
              </TableCell>

              {/* Cột 2: Tên của Zone (Clickable link to detail) */}
              <TableCell className="py-3.5 text-sm font-medium text-foreground">
                <Link to="/zones/$zoneId" params={{ zoneId: zone.id }} className="text-sm font-semibold text-primary hover:underline">
                  {zone.name}
                </Link>
              </TableCell>

              {/* Cột 3: Mã của Zone (Code) */}
              <TableCell className="py-3.5 text-sm font-medium text-muted-foreground">
                {zone.code}
              </TableCell>

              {/* Cột 4: Vị trí địa lý của Zone */}
              <TableCell className="py-3.5 text-sm font-medium text-muted-foreground whitespace-normal break-words">
                {zone.location}
              </TableCell>

              {/* Cột 5: Mô tả chi tiết của Zone */}
              <TableCell className="py-3.5 text-sm font-medium text-foreground/80 whitespace-normal break-words">
                {zone.description}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {/* Footer quản lý pagination và tùy biến page size */}
      <div className="mt-5 flex flex-col gap-3 text-sm text-muted-foreground md:flex-row md:items-center md:justify-between">
        {/* Phần hiển thị tổng quan số lượng kết quả */}
        <p>
          {totalZones === 0
            ? 'Showing 0 zones'
            : `Showing ${startIndex + 1} to ${endIndex} of ${totalZones} zones`}
        </p>

        {/* Phần nút điều khiển chuyển trang */}
        <div className="flex items-center gap-3">
          {/* Nút chuyển về trang trước */}
          <Button
            type="button"
            variant="outline"
            size="icon"
            disabled={safePage <= 1 || loading || refreshing}
            onClick={() => goToPage(safePage - 1)}
            className="size-9 rounded-lg text-muted-foreground"
          >
            <ChevronLeft className="size-4" />
          </Button>

          {/* Render danh sách các nút số trang cụ thể */}
          {Array.from({ length: totalPages }).map((_, index) => {
            const page = index + 1
            return (
              <Button
                key={page}
                type="button"
                variant="outline"
                size="icon"
                disabled={loading || refreshing}
                onClick={() => goToPage(page)}
                className={cn(
                  'size-9 rounded-lg',
                  page === safePage ? 'border-primary text-primary' : 'text-muted-foreground',
                )}
              >
                {page}
              </Button>
            )
          })}

          {/* Nút chuyển tới trang kế tiếp */}
          <Button
            type="button"
            variant="outline"
            size="icon"
            disabled={safePage >= totalPages || loading || refreshing}
            onClick={() => goToPage(safePage + 1)}
            className="size-9 rounded-lg text-muted-foreground"
          >
            <ChevronRight className="size-4" />
          </Button>

          {/* Hộp chọn thay đổi số lượng bản ghi trên một trang (Page Size Selector) */}
          <div className="relative ml-4">
            <select
              value={pageSize}
              onChange={(event) => {
                setPageSize(Number(event.target.value))
                setCurrentPage(1) // Trở về trang đầu tiên khi thay đổi kích thước trang
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
    </div>
  )
}
