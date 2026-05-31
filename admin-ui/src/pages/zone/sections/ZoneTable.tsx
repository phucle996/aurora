import { Link } from '@tanstack/react-router'
import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  MoreVertical,
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

export type ZoneStatus = 'planned' | 'active' | 'degraded' | 'maintenance' | 'disabled' | 'draining'

export type ZoneRow = {
  id: string
  code: string
  name: string
  location: string
  description: string
  status: ZoneStatus
  created_at?: string
  updated_at?: string
}

const statusLabels: Record<ZoneStatus, string> = {
  planned: 'Planned',
  active: 'Active',
  degraded: 'Degraded',
  maintenance: 'Maintenance',
  disabled: 'Disabled',
  draining: 'Draining',
}

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
  goToPage: (page: number) => void
  setPageSize: (size: number) => void
  setCurrentPage: (page: number) => void
}

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
  goToPage,
  setPageSize,
  setCurrentPage,
}: ZoneTableProps) {
  const safePage = Math.min(currentPage, totalPages)

  return (
    <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7">
      <Table>
        <TableHeader>
          <TableRow className="border-border/80 hover:bg-transparent">
            <TableHead className="w-60 px-0 pb-4 text-sm font-medium text-muted-foreground">
              Zone
            </TableHead>
            <TableHead className="pb-4 text-sm font-medium text-muted-foreground">Name</TableHead>
            <TableHead className="pb-4 text-sm font-medium text-muted-foreground">Location</TableHead>
            <TableHead className="min-w-90 pb-4 text-sm font-medium text-muted-foreground">
              Description
            </TableHead>
            <TableHead className="pb-4 text-sm font-medium text-muted-foreground">Status</TableHead>
            <TableHead className="w-22.5 pb-4 text-right text-sm font-medium text-muted-foreground">
              Actions
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {loading &&
            Array.from({ length: pageSize }).map((_, index) => (
              <TableRow key={index} className="border-border/80 hover:bg-transparent">
                {Array.from({ length: 6 }).map((__, cellIndex) => (
                  <TableCell key={cellIndex} className="py-4">
                    <Skeleton className="h-5 w-full" />
                  </TableCell>
                ))}
              </TableRow>
            ))}

          {!loading && zones.length === 0 && (
            <TableRow className="hover:bg-transparent">
              <TableCell colSpan={6} className="h-40 text-center">
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

          {!loading && zones.map((zone) => (
            <TableRow key={zone.id} className="border-border/80 hover:bg-muted/30">
              <TableCell className="px-0 py-3.5">
                <div className="flex items-center gap-5">
                  <span
                    className={cn(
                      'size-2.5 rounded-full',
                      zone.status === 'active' && 'bg-emerald-500',
                      zone.status === 'draining' && 'bg-amber-500',
                      zone.status === 'maintenance' && 'bg-violet-500',
                      zone.status === 'disabled' && 'bg-slate-400',
                      zone.status === 'planned' && 'bg-sky-500',
                      zone.status === 'degraded' && 'bg-red-500',
                    )}
                  />
                  <Link to="/zones/$zoneId" params={{ zoneId: zone.id }} className="text-sm font-semibold text-primary hover:underline">
                    {zone.code}
                  </Link>
                </div>
              </TableCell>
              <TableCell className="py-3.5 text-sm font-medium text-foreground">
                {zone.name}
              </TableCell>
              <TableCell className="py-3.5 text-sm font-medium text-muted-foreground">
                {zone.location}
              </TableCell>
              <TableCell className="py-3.5 text-sm font-medium text-foreground/80">
                {zone.description}
              </TableCell>
              <TableCell className="py-3.5">
                <StatusBadge status={zone.status} />
              </TableCell>
              <TableCell className="py-3.5 text-right">
                <button
                  type="button"
                  className="inline-flex size-9 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                  aria-label={`Open actions for ${zone.code}`}
                >
                  <MoreVertical className="size-4" />
                </button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      <div className="mt-5 flex flex-col gap-3 text-sm text-muted-foreground md:flex-row md:items-center md:justify-between">
        <p>
          {zones.length === 0
            ? 'Showing 0 zones'
            : `Showing ${startIndex + 1} to ${endIndex} of ${zones.length} zones`}
        </p>
        <div className="flex items-center gap-3">
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
    </div>
  )
}
