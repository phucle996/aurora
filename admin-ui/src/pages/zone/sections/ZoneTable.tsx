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
  MoreHorizontal,
} from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
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
  updated_at?: string
  workspacesCount?: number
  services?: string[]
  updatedText?: string
  updatedSubtext?: string
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
 * Biểu diễn trạng thái hoạt động trực quan cho từng Zone dưới dạng văn bản kèm chấm màu.
 */
export function StatusText({ status }: { status: ZoneStatus }) {
  return (
    <div className="flex items-center gap-1.5 text-xs font-semibold">
      <span className={cn(
        "size-2 rounded-full shrink-0 animate-pulse",
        status === 'active' && 'bg-emerald-500',
        status === 'draining' && 'bg-amber-500',
        status === 'maintenance' && 'bg-purple-500',
        status === 'disabled' && 'bg-slate-400',
        status === 'planned' && 'bg-sky-500',
        status === 'degraded' && 'bg-red-500',
      )} />
      <span className={cn(
        status === 'active' && 'text-emerald-600 dark:text-emerald-400',
        status === 'draining' && 'text-amber-600 dark:text-amber-400',
        status === 'maintenance' && 'text-purple-600 dark:text-purple-400',
        status === 'disabled' && 'text-slate-500 dark:text-slate-400',
        status === 'planned' && 'text-sky-600 dark:text-sky-400',
        status === 'degraded' && 'text-red-600 dark:text-red-400',
      )}>
        {statusLabels[status] ?? status}
      </span>
    </div>
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
    <div className="rounded-xl border border-border bg-card p-6 shadow-xs min-h-[600px] flex flex-col justify-between">
      {/* Khởi tạo cấu trúc bảng dữ liệu hiển thị Zone */}
      <div className="overflow-x-auto flex-1">
        <Table>
          <TableHeader>
            <TableRow className="border-border/80 hover:bg-transparent">
              <TableCell className="w-12 pb-4">
                <Checkbox />
              </TableCell>
              <TableHead className="pb-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Status
              </TableHead>
              <TableHead className="pb-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Zone
              </TableHead>
              <TableHead className="pb-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Location
              </TableHead>
              <TableHead className="pb-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Workspaces
              </TableHead>
              <TableHead className="pb-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Services
              </TableHead>
              <TableHead className="pb-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                Updated
              </TableHead>
              <TableHead className="pb-4 text-xs font-semibold uppercase tracking-wider text-muted-foreground text-right">
                Actions
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {/* Trạng thái 1: Đang tải dữ liệu - Render các dòng loading skeleton */}
            {loading &&
              Array.from({ length: pageSize }).map((_, index) => (
                <TableRow key={index} className="border-border/80 hover:bg-transparent">
                  <TableCell className="py-4">
                    <Skeleton className="h-4 w-4" />
                  </TableCell>
                  {Array.from({ length: 7 }).map((__, cellIndex) => (
                    <TableCell key={cellIndex} className="py-4">
                      <Skeleton className="h-5 w-full" />
                    </TableCell>
                  ))}
                </TableRow>
              ))}

            {/* Trạng thái 2: Không có dữ liệu - Render giao diện empty state */}
            {!loading && zones.length === 0 && (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={8} className="h-40 text-center">
                  <div className="flex flex-col items-center justify-center gap-2 text-muted-foreground py-8">
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
            {!loading && zones.map((zone) => {
              const locFirst = zone.location.includes(',') ? zone.location.split(',')[0].trim() : zone.location
              const locSecond = zone.location.includes(',') ? zone.location.split(',')[1].trim() : zone.location
              const visibleServices = zone.services ? zone.services.slice(0, zone.name.toLowerCase().includes('singapore') ? 2 : 3) : []
              const extraCount = zone.services ? zone.services.length - visibleServices.length : 0

              return (
                <TableRow key={zone.id} className="border-border/80 hover:bg-muted/30">
                  <TableCell className="py-3.5">
                    <Checkbox />
                  </TableCell>
                  <TableCell className="py-3.5">
                    <StatusText status={zone.status} />
                  </TableCell>
                  <TableCell className="py-3.5">
                    <div className="flex flex-col">
                      <Link to="/zones/$zoneId" params={{ zoneId: zone.id }} className="text-sm font-semibold text-primary hover:underline">
                        {zone.name}
                      </Link>
                      <span className="text-xs text-muted-foreground">Code: {zone.code}</span>
                    </div>
                  </TableCell>
                  <TableCell className="py-3.5">
                    <div className="flex flex-col">
                      <span className="text-sm font-medium text-foreground">{locFirst}</span>
                      <span className="text-xs text-muted-foreground">{locSecond}</span>
                    </div>
                  </TableCell>
                  <TableCell className="py-3.5">
                    <div className="flex flex-col">
                      <span className="text-sm font-bold text-foreground">{zone.workspacesCount}</span>
                      <span className="text-xs text-muted-foreground">Active</span>
                    </div>
                  </TableCell>
                  <TableCell className="py-3.5">
                    {zone.services && zone.services.length > 0 ? (
                      <div className="flex flex-wrap gap-1.5 items-center">
                        {visibleServices.map((svc) => (
                          <Badge key={svc} variant="secondary" className="px-2.5 py-0.5 text-[11px] bg-muted/65 text-muted-foreground font-semibold rounded-md border border-border/40">
                            {svc}
                          </Badge>
                        ))}
                        {extraCount > 0 && (
                          <Badge variant="outline" className="px-2.5 py-0.5 text-[11px] text-muted-foreground font-semibold border-dashed rounded-md">
                            +{extraCount}
                          </Badge>
                        )}
                      </div>
                    ) : (
                      <span className="text-xs text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell className="py-3.5">
                    <div className="flex flex-col">
                      <span className="text-sm font-medium text-foreground">{zone.updatedText}</span>
                      <span className="text-xs text-muted-foreground">{zone.updatedSubtext}</span>
                    </div>
                  </TableCell>
                  <TableCell className="py-3.5 text-right">
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon" className="h-8 w-8 rounded-lg">
                          <MoreHorizontal className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end" className="w-36">
                        <DropdownMenuItem asChild>
                          <Link to="/zones/$zoneId" params={{ zoneId: zone.id }} className="cursor-pointer">
                            Open
                          </Link>
                        </DropdownMenuItem>
                        <DropdownMenuItem asChild>
                          <Link to="/zones/$zoneId" params={{ zoneId: zone.id }} className="cursor-pointer">
                            Manage
                          </Link>
                        </DropdownMenuItem>
                        <DropdownMenuItem asChild>
                          <Link to="/zones/$zoneId" params={{ zoneId: zone.id }} className="cursor-pointer">
                            Settings
                          </Link>
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>

      {/* Footer quản lý pagination và tùy biến page size */}
      <div className="mt-5 border-t border-border pt-4 flex flex-col gap-3 text-sm text-muted-foreground md:flex-row md:items-center md:justify-between">
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
