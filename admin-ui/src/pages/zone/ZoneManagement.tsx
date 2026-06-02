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
import { usePageMeta } from '@/lib/page-meta'
import { cn } from '@/lib/utils'
import { PageContent } from '@/components/layout/layout'

import ZoneTable, { type ZoneRow, type ZoneStatus } from './sections/ZoneTable'

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
let activeZonesPromise: Promise<ZoneListResponse> | null = null

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
          return response.json() as Promise<ZoneListResponse>
        })
      }

      const payload = await activeZonesPromise
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
    const timer = setTimeout(() => {
      void loadZones()
    }, 0)
    return () => clearTimeout(timer)
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

      {/* Warning banner: zone là root topology node */}
      <div className="flex items-start gap-3 rounded-xl border border-amber-200 bg-amber-50 px-4 py-3 dark:border-amber-500/20 dark:bg-amber-500/10">
        <TriangleAlert className="mt-px size-4 shrink-0 text-amber-600 dark:text-amber-400" />
        <p className="text-sm leading-relaxed text-amber-800 dark:text-amber-300">
          <span className="font-semibold">Zone là root topology logic.</span>{' '}
          Mọi thay đổi trên zone ảnh hưởng trực tiếp đến toàn bộ hạ tầng
          dataplane. Kiểm tra kỹ trước khi thực hiện bất kỳ hành động nào.
        </p>
      </div>

      <div className="mb-6 flex flex-col gap-4 md:flex-row md:items-center md:justify-end">
        <div className="flex w-full flex-col gap-3 sm:flex-row md:max-w-140 md:justify-end">
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
        goToPage={goToPage}
        setPageSize={setPageSize}
        setCurrentPage={setCurrentPage}
      />
    </PageContent>
  )
}
