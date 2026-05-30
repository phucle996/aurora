import { useEffect, useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'

import {
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  MoreVertical,
  Plus,
  RefreshCcw,
  Search,
} from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Fetch } from '@/lib/fetch'
import { usePageMeta } from '@/lib/page-meta'
import { cn } from '@/lib/utils'
import { PageContent } from '@/components/layout/layout'

type ZoneStatus = 'active' | 'draining' | 'maintenance' | 'disabled'

type ZoneRow = {
  id: string
  code: string
  name: string
  location: string
  description: string
  status: ZoneStatus
  created_at?: string
  updated_at?: string
}

type ZoneListResponse = {
  message?: string
  error?: string
  data?: {
    items?: ZoneRow[]
    total?: number
  }
}

const statusLabels: Record<ZoneStatus, string> = {
  active: 'Active',
  draining: 'Draining',
  maintenance: 'Maintenance',
  disabled: 'Disabled',
}

function normalizeZoneStatus(value: string): ZoneStatus {
  switch (value) {
    case 'active':
    case 'draining':
    case 'maintenance':
    case 'disabled':
      return value
    default:
      return 'active'
  }
}

function StatusBadge({ status }: { status: ZoneStatus }) {
  return (
    <Badge
      variant="outline"
      className={cn(
        'h-8 rounded-lg border px-3 text-sm font-medium',
        status === 'active' && 'border-emerald-200 bg-emerald-50 text-emerald-700',
        status === 'draining' && 'border-amber-200 bg-amber-50 text-amber-700',
        status === 'maintenance' && 'border-violet-200 bg-violet-50 text-violet-700',
        status === 'disabled' && 'border-slate-200 bg-slate-50 text-slate-600',
      )}
    >
      {statusLabels[status]}
    </Badge>
  )
}

async function readErrorMessage(response: Response) {
  try {
    const payload = (await response.json()) as ZoneListResponse
    return payload.message || payload.error || 'Cannot load zones'
  } catch {
    return 'Cannot load zones'
  }
}

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

// Global Promise Deduplicator to eliminate duplicate network requests in React 18 Strict Mode/remounts.
let activeZonesPromise: Promise<any> | null = null

export default function ZoneManagementPage() {
  usePageMeta('Zone Management | Aurora Admin', 'Manage zones, statuses, and service availability across regions.')
  const [zones, setZones] = useState<ZoneRow[]>([])
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')
  const [query, setQuery] = useState('')
  const [pageSize, setPageSize] = useState(8)
  const [currentPage, setCurrentPage] = useState(1)

  async function loadZones(isSilent = false) {
    if (isSilent) {
      setRefreshing(true)
    } else {
      setLoading(true)
    }
    setError('')
    try {
      if (!activeZonesPromise) {
        activeZonesPromise = Fetch('/admin/core/zones').then(async (response) => {
          if (!response.ok) {
            const errText = await readErrorMessage(response)
            throw new Error(errText)
          }
          return response.json()
        })
      }

      const payload = (await activeZonesPromise) as ZoneListResponse
      setZones((payload.data?.items ?? []).map(normalizeZoneRow))
      if (!isSilent) {
        setCurrentPage(1)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot load zones')
      if (!isSilent) {
        setZones([])
      }
    } finally {
      activeZonesPromise = null
      setLoading(false)
      setRefreshing(false)
    }
  }

  useEffect(() => {
    void loadZones()
  }, [])

  const filteredZones = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    if (!normalizedQuery) return zones

    return zones.filter((zone) =>
      [zone.id, zone.code, zone.name, zone.location, zone.description, statusLabels[zone.status]].some((value) =>
        value.toLowerCase().includes(normalizedQuery),
      ),
    )
  }, [query, zones])

  const totalPages = Math.max(1, Math.ceil(filteredZones.length / pageSize))
  const safePage = Math.min(currentPage, totalPages)
  const startIndex = filteredZones.length === 0 ? 0 : (safePage - 1) * pageSize
  const endIndex = Math.min(startIndex + pageSize, filteredZones.length)
  const visibleZones = filteredZones.slice(startIndex, endIndex)

  const goToPage = (page: number) => {
    setCurrentPage(Math.min(Math.max(page, 1), totalPages))
  }

  return (
    <PageContent className="pb-0">
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

      <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7">
        <div className="mb-6 flex flex-col gap-4 md:flex-row md:items-center md:justify-between">
          <div className="space-y-1">
            <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Zone List</h2>
            <p className="text-sm text-muted-foreground">
              {loading || refreshing ? 'Loading real-time topology data...' : `${zones.length} zones from topology-manager`}
            </p>
          </div>
          <div className="flex w-full flex-col gap-3 sm:flex-row md:max-w-[560px] md:justify-end">
            <div className="relative flex-1">
              <Search className="pointer-events-none absolute left-4 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(event) => {
                  setQuery(event.target.value)
                  setCurrentPage(1)
                }}
                placeholder="Search zones..."
                className="h-12 rounded-lg border-border bg-background pl-11 text-sm shadow-none"
              />
            </div>
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

        {error && (
          <div className="mb-5 rounded-xl border border-destructive/20 bg-destructive/10 px-4 py-3 text-sm font-medium text-destructive">
            Cannot load zones: {error}
          </div>
        )}

        <Table>
          <TableHeader>
            <TableRow className="border-border/80 hover:bg-transparent">
              <TableHead className="w-[240px] px-0 pb-4 text-sm font-medium text-muted-foreground">
                Zone
              </TableHead>
              <TableHead className="pb-4 text-sm font-medium text-muted-foreground">Name</TableHead>
              <TableHead className="pb-4 text-sm font-medium text-muted-foreground">Location</TableHead>
              <TableHead className="min-w-[360px] pb-4 text-sm font-medium text-muted-foreground">
                Description
              </TableHead>
              <TableHead className="pb-4 text-sm font-medium text-muted-foreground">Status</TableHead>
              <TableHead className="w-[90px] pb-4 text-right text-sm font-medium text-muted-foreground">
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

            {!loading && visibleZones.length === 0 && (
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

            {!loading && visibleZones.map((zone) => (
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
            {filteredZones.length === 0
              ? 'Showing 0 zones'
              : `Showing ${startIndex + 1} to ${endIndex} of ${filteredZones.length} zones`}
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
    </PageContent>
  )
}
