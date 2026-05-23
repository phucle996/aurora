import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'

import { Link } from '@tanstack/react-router'
import {
  Activity,
  ArrowDown,
  ArrowUp,
  CalendarDays,
  ChevronDown,
  ChevronRight,
  Clock3,
  Globe2,
  Link2,
  Mail,
  Plus,
  Trash2,
  Play,
  Pause,
  Ban,
  Search,
  Server,
  ShieldCheck,
  Target,
  Users,
  Zap,
} from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Fetch } from '@/lib/fetch'
import { usePageMeta } from '@/lib/page-meta'
import { cn } from '@/lib/utils'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { TestConnectionDialog } from '@/components/smtp/TestConnectionDialog'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Skeleton } from '@/components/ui/skeleton'
import { format, subDays } from 'date-fns'
import { type DateRange } from 'react-day-picker'
import { AlertTriangle } from 'lucide-react'

type TabKey = 'overview' | 'consumers' | 'gateways' | 'endpoints'

type Tone = 'blue' | 'green' | 'purple' | 'amber' | 'red' | 'slate'

type Metric = {
  label: string
  value: string
  sub?: string
  delta?: string
  trend: 'up' | 'down'
  icon: ReactNode
  tone: Tone
}

type SMTPOverviewResponse = {
  metrics: {
    delivered_today: number
    queued_now: number
    active_consumers: number
    total_consumers: number
    active_gateways: number
    total_gateways: number
    delivery_success_rate: number
  }
  delivery_throughput: Array<{ label: string; delivered: number; queued: number; retries: number }> | null
  health_distribution: { healthy: number; warning: number; stopped: number; unknown: number }
  top_organizations: Array<{ tenant_id: string; delivered: number; total_attempts: number; success_rate: number; queued: number }> | null
  zone_health: Array<{ zone_id: string; healthy: number; degraded: number; unhealthy: number; total: number; status: string }> | null
  timeline: Array<{ id: string; entity_type: string; entity_name: string; action: string; actor_name: string; note: string; created_at: string }> | null
  insights: Array<{ title: string; value: string; note: string; tone: string }> | null
}

type APIResponse<T> = {
  data?: T
  message?: string
  error?: string
}

type OverviewState = {
  loading: boolean
  error: string
  data: SMTPOverviewResponse | null
}

type ConsumerAggregationResponse = {
  metrics: { total_consumers: number; active_consumers: number; disabled_consumers: number; total_lag: number; avg_worker_concurrency: number; avg_ack_timeout_seconds: number }
  status: Array<{ status: string; count: number }> | null
  shard_states: Array<{ state: string; count: number; lag: number }> | null
  organizations: Array<{ tenant_id: string; label: string; total: number; active: number; disabled: number; total_lag: number; workspace_id: string }> | null
  workspace_summary: { workspace_id: string; total_consumers: number; active_consumers: number; disabled_consumers: number; total_lag: number; tenant_count: number } | null
  lagging_consumers: Array<{ id: string; name: string; tenant_id: string; lag: number; status: string; updated_at: string }> | null
  items: ConsumerListItem[] | null
}

type ConsumerListItem = { id: string; workspace_id: string; tenant_id: string; zone_id: string; name: string; transport_type: string; source: string; consumer_group: string; worker_concurrency: number; desired_shard_count: number; lag: number; status: string; updated_at: string }

type GatewayAggregationResponse = {
  metrics: { total_gateways: number; active_gateways: number; disabled_gateways: number; ready_shards: number; pending_shards: number; draining_shards: number; bound_endpoints: number }
  status: Array<{ status: string; count: number }> | null
  shard_states: Array<{ state: string; count: number; lag: number }> | null
  traffic_classes: Array<{ traffic_class: string; total_gateways: number; active_gateways: number; ready_shards: number; endpoint_count: number }> | null
  alerts: Array<{ title: string; gateway_id: string; gateway_name: string; severity: string; note: string; updated_at: string }> | null
  items: GatewayListItem[] | null
}

type GatewayListItem = { id: string; workspace_id: string; tenant_id: string; zone_id: string; name: string; traffic_class: string; status: string; routing_mode: string; desired_shard_count: number; endpoint_count: number; ready_shards: number; pending_shards: number; draining_shards: number; updated_at: string }

type EndpointListItem = { id: string; name: string; host: string; port: number; max_connections: number; status: string; tls_mode: string; has_secret: boolean; updated_at: string }

type ResourceState<T> = { loading: boolean; error: string; data: T | null }

const tabs: Array<{ key: TabKey; label: string; description: string; icon: ReactNode }> = [
  { key: 'overview', label: 'Overview', description: 'SMTP overview', icon: <Activity className="size-4" /> },
  { key: 'consumers', label: 'Consumers', description: 'SMTP consumers & usage', icon: <Users className="size-4" /> },
  { key: 'gateways', label: 'Gateways', description: 'SMTP gateways & routing', icon: <Server className="size-4" /> },
  { key: 'endpoints', label: 'Endpoints', description: 'SMTP endpoints & targets', icon: <Link2 className="size-4" /> },
]

const toneClass: Record<Tone, string> = {
  blue: 'bg-primary/10 text-primary',
  green: 'bg-emerald-500/10 text-emerald-600',
  purple: 'bg-violet-500/10 text-violet-600',
  amber: 'bg-amber-500/10 text-amber-600',
  red: 'bg-destructive/10 text-destructive',
  slate: 'bg-slate-500/10 text-muted-foreground',
}

const fallbackChartData = Array.from({ length: 24 }, () => 0)

function PageShell() {
  usePageMeta('SMTP Admin | Aurora Admin', 'Monitor SMTP traffic, endpoints, gateways, and runtime aggregation.')
  const [active, setActive] = useState<TabKey>(() => window.location.hash === '#endpoints' ? 'endpoints' : 'overview')
  const [zones, setZones] = useState<Array<{ id: string; name: string }>>([])
  const [selectedZone, setSelectedZone] = useState<string | null>(null)
  const showsZoneFilter = active !== 'endpoints'
  const showsDateRangeFilter = active === 'overview' || active === 'consumers'

  useEffect(() => {
    if (!showsZoneFilter || zones.length > 0) {
      return
    }
    async function loadZones() {
      try {
        const resp = await Fetch('/admin/zones')
        if (resp.ok) {
          const body = await resp.json()
          setZones(body.data?.items || [])
        }
      } catch (err) {
        console.error('Failed to load zones', err)
      }
    }
    void loadZones()
  }, [showsZoneFilter, zones.length])

  const [dateRange, setDateRange] = useState<DateRange | undefined>({
    from: subDays(new Date(), 7),
    to: new Date(),
  })

  const activeZoneLabel = useMemo(() => {
    if (!selectedZone) return 'All Zones'
    return zones.find(z => z.id === selectedZone)?.name || selectedZone
  }, [selectedZone, zones])

  const dateRangeLabel = useMemo(() => {
    if (!dateRange?.from) return 'Select range'
    if (!dateRange.to) return format(dateRange.from, 'LLL d, y')
    return `${format(dateRange.from, 'LLL d')} – ${format(dateRange.to, 'LLL d, y')}`
  }, [dateRange])

  return (
    <div className="min-w-0 space-y-4 pb-8">
      <div className="flex flex-col gap-4 xl:flex-row xl:items-start xl:justify-between">
        <div className="space-y-1">
          <h1 className="aurora-page-title">SMTP Admin</h1>
          <p className="aurora-page-subtitle">
            Platform-wide visibility for SMTP delivery, routing, and infrastructure health across all organizations.
          </p>
        </div>
        {showsZoneFilter || showsDateRangeFilter ? (
          <div className="flex flex-wrap items-center gap-3">
            {showsZoneFilter ? (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline" className="h-10 min-w-36 justify-between gap-3 border-border/80 bg-card px-4 aurora-filter-text shadow-sm">
                    <span className="flex items-center gap-2">
                      <Globe2 className="size-4" />
                      {activeZoneLabel}
                    </span>
                    <ChevronDown className="size-4 text-muted-foreground" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-56">
                  <DropdownMenuItem onClick={() => setSelectedZone(null)}>
                    All Zones
                  </DropdownMenuItem>
                  {zones.map((zone) => (
                    <DropdownMenuItem key={zone.id} onClick={() => setSelectedZone(zone.id)}>
                      {zone.name}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            ) : null}

            {showsDateRangeFilter ? (
              <Popover>
                <PopoverTrigger asChild>
                  <Button variant="outline" className="h-10 min-w-44 justify-between gap-3 border-border/80 bg-card px-4 aurora-filter-text shadow-sm">
                    <span className="flex items-center gap-2">
                      <CalendarDays className="size-4" />
                      {dateRangeLabel}
                    </span>
                    <ChevronDown className="size-4 text-muted-foreground" />
                  </Button>
                </PopoverTrigger>
                <PopoverContent className="w-auto p-0" align="end">
                  <Calendar
                    initialFocus
                    mode="range"
                    defaultMonth={dateRange?.from}
                    selected={dateRange}
                    onSelect={setDateRange}
                    numberOfMonths={2}
                  />
                </PopoverContent>
              </Popover>
            ) : null}
          </div>
        ) : null}
      </div>

      <div className="rounded-xl border border-border bg-card p-1 shadow-sm">
        <div className="grid grid-cols-2 gap-1 lg:grid-cols-4">
          {tabs.map((tab) => (
            <button
              key={tab.key}
              type="button"
              onClick={() => setActive(tab.key)}
              className={cn(
                'flex items-center gap-3 rounded-lg px-4 py-3 text-left transition-colors',
                active === tab.key ? 'bg-primary/5 text-primary ring-1 ring-primary/15' : 'text-muted-foreground hover:bg-muted/70 hover:text-foreground',
              )}
            >
              <span className={cn('inline-flex size-8 items-center justify-center rounded-full', active === tab.key ? 'bg-primary/10' : 'bg-muted')}>
                {tab.icon}
              </span>
              <span>
                <span className="aurora-tab-label">{tab.label}</span>
                <span className="aurora-tab-description">{tab.description}</span>
              </span>
            </button>
          ))}
        </div>
      </div>

      {active === 'overview' && <OverviewTab zoneID={selectedZone} dateRange={dateRange} />}
      {active === 'consumers' && <ConsumersTab zoneID={selectedZone} dateRange={dateRange} />}
      {active === 'gateways' && <GatewaysTab zoneID={selectedZone} />}
      {active === 'endpoints' && <EndpointsTab />}
    </div>
  )
}

function usePollingResource<T>(loader: (signal: AbortSignal) => Promise<T>, options: { poll?: boolean } = {}): ResourceState<T> {
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
        if (!cancelled) setState({ loading: false, error: error instanceof Error ? error.message : 'Cannot load SMTP data.', data: null })
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

async function readAPIData<T>(resp: Response): Promise<T> {
  if (!resp.ok) throw new Error('Cannot load SMTP data.')
  const body = (await resp.json()) as APIResponse<T>
  if (!body.data) throw new Error('SMTP response is empty.')
  return body.data
}

function FilterButton({ icon, label }: { icon: ReactNode; label: string }) {
  return (
    <Button variant="outline" className="h-10 min-w-36 justify-between gap-3 border-border/80 bg-card px-4 aurora-filter-text shadow-sm">
      <span className="flex items-center gap-2">{icon}{label}</span>
      <ChevronDown className="size-4 text-muted-foreground" />
    </Button>
  )
}

function MetricGrid({ metrics }: { metrics: Metric[] }) {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-6">
      {metrics.map((metric) => (
        <MetricCard key={metric.label} {...metric} />
      ))}
    </div>
  )
}

function MetricCard({ label, value, sub, delta, trend, icon, tone }: Metric) {
  const TrendIcon = trend === 'up' ? ArrowUp : ArrowDown
  return (
    <section className="rounded-xl border border-border bg-card p-4 shadow-sm">
      <div className="flex items-center gap-3">
        <span className={cn('inline-flex size-12 shrink-0 items-center justify-center rounded-full', toneClass[tone])}>{icon}</span>
        <div className="min-w-0">
          <p className="aurora-card-label">{label}</p>
          <p className="mt-1 aurora-metric-value">{value}</p>
          {sub ? <p className="aurora-caption">{sub}</p> : null}
        </div>
      </div>
      {delta ? (
        <div className="mt-3 flex items-center gap-1 text-xs font-semibold">
          <TrendIcon className={cn('size-3.5', trend === 'up' ? 'text-emerald-600' : 'text-destructive')} />
          <span className={trend === 'up' ? 'text-emerald-600' : 'text-destructive'}>{delta}</span>
          <span className="font-medium text-muted-foreground">live DB</span>
        </div>
      ) : (
        <div className="mt-3 text-xs font-medium text-muted-foreground">live DB</div>
      )}
    </section>
  )
}

function Panel({ title, action, children, className }: { title: string; action?: ReactNode; children: ReactNode; className?: string }) {
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

function LineChart({ secondary = true, tertiary = false, delivered = fallbackChartData, queued = fallbackChartData, retries = fallbackChartData, labels }: { secondary?: boolean; tertiary?: boolean; delivered?: number[]; queued?: number[]; retries?: number[]; labels?: string[] }) {
  const path = linePath(delivered)
  const path2 = linePath(queued)
  const path3 = linePath(retries)
  const axisLabels = labels && labels.length > 0 ? labels : ['00:00', '06:00', '12:00', '18:00', '24:00']
  const labelAt = (index: number, fallback: string) => axisLabels[Math.min(index, axisLabels.length - 1)] ?? fallback
  return (
    <div className="h-[230px] w-full rounded-lg bg-gradient-to-b from-primary/5 to-transparent px-2 pb-2 pt-1">
      <div className="mb-2 flex items-center gap-5 pl-8 aurora-table-cell">
        <Legend color="bg-primary" label="Delivered" />
        {secondary ? <Legend color="bg-primary/70" label="Queued" dashed /> : null}
        {tertiary ? <Legend color="bg-emerald-500" label="Retries" dashed /> : null}
      </div>
      <svg viewBox="0 0 640 180" className="h-[180px] w-full overflow-visible">
        {[0, 45, 90, 135, 180].map((y) => <line key={y} x1="48" x2="625" y1={y} y2={y} stroke="hsl(var(--border))" strokeOpacity="0.65" />)}
        <path d={path} fill="none" stroke="hsl(var(--primary))" strokeWidth="3" strokeLinejoin="round" strokeLinecap="round" />
        {secondary ? <path d={path2} fill="none" stroke="hsl(var(--primary))" strokeDasharray="6 5" strokeWidth="2.5" strokeLinejoin="round" strokeLinecap="round" opacity=".8" /> : null}
        {tertiary ? <path d={path3} fill="none" stroke="#10b981" strokeDasharray="6 5" strokeWidth="2.5" strokeLinejoin="round" strokeLinecap="round" /> : null}
        <text x="48" y="176" className="fill-muted-foreground text-[12px]">{labelAt(0, '00:00')}</text>
        <text x="190" y="176" className="fill-muted-foreground text-[12px]">{labelAt(Math.floor(axisLabels.length * 0.25), '06:00')}</text>
        <text x="335" y="176" className="fill-muted-foreground text-[12px]">{labelAt(Math.floor(axisLabels.length * 0.5), '12:00')}</text>
        <text x="480" y="176" className="fill-muted-foreground text-[12px]">{labelAt(Math.floor(axisLabels.length * 0.75), '18:00')}</text>
        <text x="600" y="176" className="fill-muted-foreground text-[12px]">{labelAt(axisLabels.length - 1, '24:00')}</text>
      </svg>
    </div>
  )
}

function linePath(points: number[]) {
  const safePoints = points.length > 0 ? points : fallbackChartData
  const max = Math.max(...safePoints)
  const min = Math.min(...safePoints)
  const xStep = 575 / Math.max(safePoints.length - 1, 1)
  return safePoints
    .map((point, index) => {
      const x = 48 + index * xStep
      const y = 150 - ((point - min) / Math.max(max - min, 1)) * 120
      return `${index === 0 ? 'M' : 'L'} ${x.toFixed(1)} ${y.toFixed(1)}`
    })
    .join(' ')
}

function Legend({ color, label, dashed }: { color: string; label: string; dashed?: boolean }) {
  return <span className="inline-flex items-center gap-2"><span className={cn('h-0.5 w-5 rounded-full', color, dashed && 'bg-transparent border-t-2 border-dashed border-current text-primary')} />{label}</span>
}

function Donut({ total, segments, centerLabel = 'Total' }: { total: string; segments: Array<{ label: string; value: string; color: string }>; centerLabel?: string }) {
  const gradient = useMemo(() => {
    const step = 100 / segments.length
    return segments.map((s, i) => `${s.color} ${i * step}% ${(i + 1) * step}%`).join(', ')
  }, [segments])
  return (
    <div className="flex items-center justify-center gap-8">
      <div className="relative size-40 rounded-full" style={{ background: `conic-gradient(${gradient})` }}>
        <div className="absolute inset-8 flex flex-col items-center justify-center rounded-full bg-card shadow-inner">
          <span className="aurora-metric-value">{total}</span>
          <span className="text-xs font-medium text-muted-foreground">{centerLabel}</span>
        </div>
      </div>
      <div className="space-y-3 text-sm">
        {segments.map((segment) => (
          <div key={segment.label} className="flex min-w-44 items-center justify-between gap-4">
            <span className="inline-flex items-center gap-2 font-medium text-muted-foreground"><span className="size-2 rounded-full" style={{ background: segment.color }} />{segment.label}</span>
            <span className="font-semibold text-foreground">{segment.value}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function OverviewTab({ zoneID, dateRange }: { zoneID: string | null; dateRange: DateRange | undefined }) {
  const [state, setState] = useState<OverviewState>({ loading: true, error: '', data: null })

  useEffect(() => {
    let cancelled = false
    const controller = new AbortController()

    async function loadOverview() {
      setState((prev) => ({ ...prev, loading: true, error: '' }))
      try {
        const params = new URLSearchParams()
        if (zoneID) params.set('zone_id', zoneID)
        if (dateRange?.from) params.set('start_at', dateRange.from.toISOString())
        if (dateRange?.to) params.set('end_at', dateRange.to.toISOString())

        const query = params.toString()
        const url = `/admin/smtp/aggregation${query ? `?${query}` : ''}`

        const resp = await Fetch(url, { signal: controller.signal })
        if (!resp.ok) {
          throw new Error('Cannot load SMTP overview.')
        }
        const body = (await resp.json()) as APIResponse<SMTPOverviewResponse>
        if (!body.data) {
          throw new Error('SMTP overview response is empty.')
        }
        if (!cancelled) {
          setState({ loading: false, error: '', data: body.data })
        }
      } catch (error) {
        if (error instanceof DOMException && error.name === 'AbortError') return
        if (!cancelled) {
          setState({ loading: false, error: error instanceof Error ? error.message : 'Cannot load SMTP overview.', data: null })
        }
      }
    }

    const timeoutID = window.setTimeout(() => void loadOverview(), 0)
    return () => {
      cancelled = true
      controller.abort()
      window.clearTimeout(timeoutID)
    }
  }, [zoneID, dateRange])

  if (state.loading) {
    return <OverviewSkeleton />
  }

  if (state.error || !state.data) {
    return <OverviewError message={state.error || 'SMTP overview data is unavailable.'} />
  }

  const overview = state.data
  const throughput = overview.delivery_throughput ?? []
  const delivered = throughput.map((point) => point.delivered)
  const queued = throughput.map((point) => point.queued)
  const retries = throughput.map((point) => point.retries)
  const labels = throughput.map((point) => point.label)
  const totalComponents = overview.health_distribution.healthy + overview.health_distribution.warning + overview.health_distribution.stopped + overview.health_distribution.unknown
  const health = overview.health_distribution
  const healthSegments = [
    { label: 'Healthy', value: formatSegmentValue(health.healthy, totalComponents), color: '#22c55e' },
    { label: 'Degraded', value: formatSegmentValue(health.warning, totalComponents), color: '#f59e0b' },
    { label: 'Unhealthy', value: formatSegmentValue(health.stopped, totalComponents), color: '#ef4444' },
    { label: 'Unknown', value: formatSegmentValue(health.unknown, totalComponents), color: '#94a3b8' },
  ]

  return (
    <div className="space-y-3">
      <MetricGrid metrics={[
        { label: 'Delivered (24h)', value: formatCompactNumber(overview.metrics.delivered_today), icon: <Mail className="size-5" />, tone: 'blue', trend: 'up' },
        { label: 'Queue Depth', value: formatCompactNumber(overview.metrics.queued_now), icon: <Activity className="size-5" />, tone: 'purple', trend: 'down' },
        { label: 'Active Consumers', value: formatNumber(overview.metrics.active_consumers), sub: `${formatNumber(overview.metrics.total_consumers)} total`, icon: <Users className="size-5" />, tone: 'green', trend: 'up' },
        { label: 'Active Gateways', value: formatNumber(overview.metrics.active_gateways), sub: `${formatNumber(overview.metrics.total_gateways)} total`, icon: <Server className="size-5" />, tone: 'blue', trend: 'up' },
        { label: 'Delivery Success Rate', value: `${overview.metrics.delivery_success_rate.toFixed(2)}%`, icon: <Target className="size-5" />, tone: 'green', trend: 'up' },
      ]} />
      <div className="grid gap-3 xl:grid-cols-[1.4fr_.72fr_1.12fr]">
        <Panel title="Global Delivery Throughput (All Organizations)" action={<FilterButton icon={null} label="24h" />}><LineChart tertiary delivered={delivered} queued={queued} retries={retries} labels={labels} /></Panel>
        <Panel title="Endpoint Health Distribution"><Donut total={formatNumber(totalComponents)} centerLabel="Endpoints" segments={healthSegments} /></Panel>
        <Panel title="Top Organizations by Delivery Volume (24h)" action={<a className="aurora-link-text">View all organizations</a>}><OrganizationTable rows={overview.top_organizations ?? []} /></Panel>
      </div>
      <div className="grid gap-3 xl:grid-cols-3">
        <Panel title="Zone Health Summary"><CompactTable rows={zoneHealthRows(overview.zone_health ?? [])} /></Panel>
        <Panel title="Recent Incidents & Changes" action={<a className="aurora-link-text">View all</a>}><TimelineList rows={overview.timeline ?? []} /></Panel>
        <Panel title="Operational Insights"><OverviewInsights rows={overview.insights ?? []} /></Panel>
      </div>
    </div>
  )
}

function ConsumersTab({ zoneID, dateRange }: { zoneID: string | null; dateRange: DateRange | undefined }) {
  const [scope, setScope] = useState<'organization' | 'workspace'>('organization')
  const loadConsumers = useCallback(async (signal: AbortSignal) => {
    const params = new URLSearchParams()
    if (zoneID) params.set('zone_id', zoneID)
    if (dateRange?.from) params.set('start_at', dateRange.from.toISOString())
    if (dateRange?.to) params.set('end_at', dateRange.to.toISOString())
    const query = params.toString()

    const aggregation = await Fetch(`/admin/smtp/aggregation/consumers${query ? `?${query}` : ''}`, { signal }).then((resp) => readAPIData<ConsumerAggregationResponse>(resp))
    return { aggregation, items: aggregation.items ?? [] }
  }, [zoneID, dateRange])
  const state = usePollingResource(loadConsumers)

  if (state.loading) return <OverviewSkeleton />
  if (state.error || !state.data) return <OverviewError message={state.error || 'Consumer data is unavailable.'} />

  const { aggregation, items } = state.data
  const metrics = aggregation.metrics
  const statusTotal = (aggregation.status ?? []).reduce((sum, item) => sum + item.count, 0)
  const statusSegments = statusSegmentsFromCounts(aggregation.status ?? [], statusTotal)

  return (
    <div className="space-y-3">
      <MetricGrid metrics={[
        { label: 'Total Consumers', value: formatNumber(metrics.total_consumers), trend: 'up', icon: <Users className="size-5" />, tone: 'blue' },
        { label: 'Active Consumers', value: formatNumber(metrics.active_consumers), sub: `${formatNumber(metrics.disabled_consumers)} disabled`, trend: 'up', icon: <ShieldCheck className="size-5" />, tone: 'green' },
        { label: 'Avg Worker Concurrency', value: metrics.avg_worker_concurrency.toFixed(1), trend: 'up', icon: <Activity className="size-5" />, tone: 'purple' },
        { label: 'Consumer Lag Queue', value: formatCompactNumber(metrics.total_lag), sub: 'msgs', trend: 'down', icon: <Clock3 className="size-5" />, tone: 'amber' },
        { label: 'Avg Ack Timeout', value: `${metrics.avg_ack_timeout_seconds.toFixed(1)}s`, trend: 'down', icon: <Clock3 className="size-5" />, tone: 'red' },
        { label: 'Organizations', value: formatNumber(aggregation.workspace_summary?.tenant_count ?? 0), sub: 'with tenant_id', trend: 'up', icon: <Target className="size-5" />, tone: 'blue' },
      ]} />
      <div className="grid gap-3 xl:grid-cols-[1.2fr_.86fr_1fr]">
        <Panel title="Consumer Count by Organization"><ConsumerOrganizationRows rows={aggregation.organizations ?? []} /></Panel>
        <Panel title="Consumer Status Distribution"><Donut total={formatNumber(statusTotal)} segments={statusSegments} /></Panel>
        <Panel title="Consumer Shard States"><CompactTable rows={consumerShardRows(aggregation.shard_states ?? [])} /></Panel>
      </div>
      <Panel title="Consumer Scope" action={<div className="flex rounded-lg border border-border bg-muted/50 p-1"><button className={cn('rounded-md px-3 py-1 text-xs font-semibold', scope === 'organization' && 'bg-card text-primary shadow-sm')} onClick={() => setScope('organization')}>By Organization</button><button className={cn('rounded-md px-3 py-1 text-xs font-semibold', scope === 'workspace' && 'bg-card text-primary shadow-sm')} onClick={() => setScope('workspace')}>By Workspace</button></div>}>
        {scope === 'organization' ? <ConsumerOrganizationRows rows={aggregation.organizations ?? []} /> : <WorkspaceSummary row={aggregation.workspace_summary} />}
      </Panel>
      <div className="grid gap-3 xl:grid-cols-[1fr_1fr]">
        <Panel title="Lagging Consumers (by Highest Lag)"><CompactTable rows={(aggregation.lagging_consumers ?? []).map((row) => [row.name, row.tenant_id || 'No organization', formatCompactNumber(row.lag), statusLabel(row.status)])} /></Panel>
        <Inventory title="Consumer Fleet Inventory" rows={items.map((row) => [row.name, row.tenant_id || 'No organization', row.source, row.transport_type, String(row.worker_concurrency), String(row.desired_shard_count), formatCompactNumber(row.lag), statusLabel(row.status), formatRelativeTime(row.updated_at)])} columns={['Name', 'Organization', 'Source', 'Transport', 'Worker Concurrency', 'Desired Shards', 'Lag', 'Status', 'Updated']} />
      </div>
    </div>
  )
}

function GatewaysTab({ zoneID }: { zoneID: string | null }) {
  const loadGateways = useCallback(async (signal: AbortSignal) => {
    const params = new URLSearchParams()
    if (zoneID) params.set('zone_id', zoneID)
    const query = params.toString()
    const aggregation = await Fetch(`/admin/smtp/aggregation/gateways${query ? `?${query}` : ''}`, { signal }).then((resp) => readAPIData<GatewayAggregationResponse>(resp))
    return { aggregation, items: aggregation.items ?? [] }
  }, [zoneID])
  const state = usePollingResource(loadGateways)

  if (state.loading) return <OverviewSkeleton />
  if (state.error || !state.data) return <OverviewError message={state.error || 'Gateway data is unavailable.'} />

  const { aggregation, items } = state.data
  const metrics = aggregation.metrics
  const shardTotal = metrics.ready_shards + metrics.pending_shards + metrics.draining_shards
  const statusTotal = (aggregation.status ?? []).reduce((sum, item) => sum + item.count, 0)

  return (
    <div className="space-y-3">
      <MetricGrid metrics={[
        { label: 'Total Gateways', value: formatNumber(metrics.total_gateways), trend: 'up', icon: <Server className="size-5" />, tone: 'purple' },
        { label: 'Healthy Gateways', value: formatNumber(metrics.active_gateways), sub: `${formatNumber(metrics.disabled_gateways)} disabled`, trend: 'up', icon: <ShieldCheck className="size-5" />, tone: 'green' },
        { label: 'Ready Shards', value: formatNumber(metrics.ready_shards), trend: 'up', icon: <Server className="size-5" />, tone: 'blue' },
        { label: 'Pending + Draining Shards', value: formatNumber(metrics.pending_shards + metrics.draining_shards), trend: 'down', icon: <Clock3 className="size-5" />, tone: 'amber' },
        { label: 'Active Endpoint Pool', value: formatNumber(metrics.bound_endpoints), sub: 'global routing pool', trend: 'up', icon: <Link2 className="size-5" />, tone: 'blue' },
        { label: 'Avg Delivery Latency', value: 'N/A', sub: 'not tracked in DB', trend: 'down', icon: <Target className="size-5" />, tone: 'slate' },
      ]} />
      <div className="grid gap-3 xl:grid-cols-[.82fr_.82fr_1fr]">
        <Panel title="Gateway Status Distribution"><Donut total={formatNumber(statusTotal)} segments={statusSegmentsFromCounts(aggregation.status ?? [], statusTotal)} /></Panel>
        <Panel title="Shard State Distribution"><Donut total={formatNumber(shardTotal)} centerLabel="Total Shards" segments={gatewayShardSegments(aggregation.shard_states ?? [], shardTotal)} /></Panel>
        <Panel title="Traffic Class Summary"><CompactTable rows={(aggregation.traffic_classes ?? []).map((row) => [row.traffic_class, formatNumber(row.total_gateways), formatNumber(row.active_gateways), formatNumber(row.ready_shards)])} /></Panel>
      </div>
      <div className="grid gap-3 xl:grid-cols-[1fr_1fr]">
        <Panel title="Gateway Alerts"><CompactTable rows={(aggregation.alerts ?? []).map((row) => [row.title, row.gateway_name, row.severity, formatRelativeTime(row.updated_at)])} /></Panel>
        <Inventory title="Gateway Fleet Inventory" rows={items.map((row) => [row.name, row.tenant_id || 'All Organizations', row.zone_id || '-', row.traffic_class, row.routing_mode, formatNumber(row.ready_shards), formatNumber(row.pending_shards), formatNumber(row.draining_shards), statusLabel(row.status), formatRelativeTime(row.updated_at)])} columns={['Name', 'Organization Scope', 'Zone', 'Traffic Class', 'Routing Mode', 'Ready', 'Pending', 'Draining', 'Status', 'Updated']} />
      </div>
    </div>
  )
}

function EndpointsTab() {
  const [refreshKey, setRefreshKey] = useState(0)
  const loadEndpoints = useCallback(async (signal: AbortSignal) => {
    void refreshKey
    const list = await Fetch('/admin/endpoints', { signal }).then((resp) => readAPIData<{ items: EndpointListItem[] }>(resp))
    return { items: list.items ?? [] }
  }, [refreshKey])
  const state = usePollingResource(loadEndpoints, { poll: false })


  const deleteEndpoint = async (endpoint: EndpointListItem) => {
    if (!window.confirm(`Delete endpoint ${endpoint.name}?`)) return
    const resp = await Fetch(`/admin/endpoints/${endpoint.id}`, { method: 'DELETE' })
    if (!resp.ok) {
      window.alert(await readAPIMessage(resp, 'Cannot delete endpoint.'))
      return
    }
    setRefreshKey((value) => value + 1)
  }

  const [connState, setConnState] = useState<{
    isOpen: boolean;
    loading: boolean;
    success: boolean | null;
    message: string;
    endpointName: string;
  }>({
    isOpen: false,
    loading: false,
    success: null,
    message: '',
    endpointName: '',
  })

  const tryConnect = async (endpoint: EndpointListItem) => {
    setConnState({
      isOpen: true,
      loading: true,
      success: null,
      message: 'Establishing connection to SMTP server...',
      endpointName: endpoint.name,
    })

    try {
      const resp = await Fetch('/admin/endpoints/try-connect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: endpoint.id }),
      })

      if (resp.ok) {
        setConnState(prev => ({
          ...prev,
          loading: false,
          success: true,
          message: 'Connection successful! The SMTP server is reachable and credentials are valid.',
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

  const updateStatus = async (endpoint: EndpointListItem, status: string) => {
    try {
      const resp = await Fetch(`/admin/endpoints/${endpoint.id}/status`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ status }),
      })
      if (!resp.ok) throw new Error(await readAPIMessage(resp, 'Cannot update status.'))
      setRefreshKey((v) => v + 1)
    } catch (err) {
      console.error(err)
      alert(err instanceof Error ? err.message : 'Cannot update status.')
    }
  }

  if (state.loading) return <OverviewSkeleton />
  if (state.error || !state.data) return <OverviewError message={state.error || 'Endpoint data is unavailable.'} />

  const items = state.data.items

  return (
    <div className="space-y-3">
      <Panel title="Endpoint Fleet" action={<Button asChild className="h-9 font-bold"><Link to="/smtp/endpoints/new"><Plus className="size-4" />Add Endpoint</Link></Button>}>
        {items.length === 0 ? (
          <EmptyState title="No SMTP endpoints" description="Create an endpoint to start routing SMTP delivery." />
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Host</TableHead>
                <TableHead>TLS</TableHead>
                <TableHead>Capacity</TableHead>
                <TableHead>Secret</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Updated</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="font-semibold">{item.name}</TableCell>
                  <TableCell>{item.host}:{item.port}</TableCell>
                  <TableCell>{statusLabel(item.tls_mode)}</TableCell>
                  <TableCell>{formatNumber(item.max_connections)}</TableCell>
                  <TableCell>{item.has_secret ? 'Configured' : 'Missing'}</TableCell>
                  <TableCell><StatusBadge value={statusLabel(item.status)} /></TableCell>
                  <TableCell>{formatRelativeTime(item.updated_at)}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-2">
                      {item.status !== 'active' && (
                        <Button variant="outline" size="icon-sm" onClick={() => void updateStatus(item, 'active')} title="Activate">
                          <Play className="size-4 text-emerald-500 fill-emerald-500/20" />
                        </Button>
                      )}
                      {item.status === 'active' && (
                        <Button variant="outline" size="icon-sm" onClick={() => void updateStatus(item, 'suspended')} title="Suspend">
                          <Pause className="size-4 text-amber-500 fill-amber-500/20" />
                        </Button>
                      )}
                      {item.status !== 'disabled' && (
                        <Button variant="outline" size="icon-sm" onClick={() => void updateStatus(item, 'disabled')} title="Disable">
                          <Ban className="size-4 text-slate-400" />
                        </Button>
                      )}
                      <Button variant="outline" size="sm" onClick={() => void tryConnect(item)}>Try</Button>
                      <Button variant="destructive" size="icon-sm" onClick={() => void deleteEndpoint(item)} aria-label={`Delete ${item.name}`}><Trash2 className="size-4" /></Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Panel>

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

function OrganizationTable({ rows }: { rows: NonNullable<SMTPOverviewResponse['top_organizations']> }) {
  if (rows.length === 0) {
    return <EmptyState title="No organization delivery yet" description="Records without tenant_id are valid, but they are not grouped into organizations." />
  }
  return <CompactTable rows={rows.map((row, index) => [String(index + 1), row.tenant_id, formatCompactNumber(row.delivered), `${row.success_rate.toFixed(2)}%`, formatCompactNumber(row.queued)])} />
}

function TimelineList({ rows }: { rows: NonNullable<SMTPOverviewResponse['timeline']> }) {
  if (rows.length === 0) {
    return <EmptyState title="No recent changes" description="Activity logs will appear here when SMTP resources change." />
  }
  return <InsightRows rows={rows.map((row) => [row.entity_name || row.entity_type, row.action, row.note || formatRelativeTime(row.created_at)])} />
}

function OverviewInsights({ rows }: { rows: NonNullable<SMTPOverviewResponse['insights']> }) {
  if (rows.length === 0) {
    return <EmptyState title="No insights yet" description="Insights are generated after delivery attempts or resource status changes exist." />
  }
  return <InsightRows rows={rows.map((row) => [row.title, row.value, row.note])} />
}

function InsightRows({ rows }: { rows: string[][] }) {
  return <div className="divide-y divide-border/70">{rows.map((row, i) => <div key={row.join('-')} className="flex items-center gap-3 py-3"><span className={cn('inline-flex size-9 items-center justify-center rounded-full', ['bg-primary/10 text-primary', 'bg-emerald-500/10 text-emerald-600', 'bg-amber-500/10 text-amber-600', 'bg-violet-500/10 text-violet-600'][i % 4])}>{i % 3 === 0 ? <Zap className="size-4" /> : i % 3 === 1 ? <ShieldCheck className="size-4" /> : <AlertTriangle className="size-4" />}</span><div className="min-w-0 flex-1"><p className="aurora-insight-title">{row[0]}</p><p className="text-xs font-medium text-muted-foreground">{row[1]} {row[2]}</p></div><ChevronRight className="size-4 text-muted-foreground" /></div>)}</div>
}

function EmptyState({ title, description }: { title: string; description: string }) {
  return <div className="rounded-lg border border-dashed border-border bg-muted/30 p-6 text-center"><p className="aurora-insight-title">{title}</p><p className="mt-1 aurora-insight-meta">{description}</p></div>
}

function OverviewSkeleton() {
  return <div className="grid gap-3 xl:grid-cols-3">{Array.from({ length: 6 }).map((_, index) => <Skeleton key={index} className="h-28 rounded-xl border border-border" />)}</div>
}

function OverviewError({ message }: { message: string }) {
  return <Panel title="SMTP Overview unavailable"><EmptyState title="Cannot load SMTP overview" description={message} /></Panel>
}

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

function zoneHealthRows(rows: NonNullable<SMTPOverviewResponse['zone_health']>) {
  if (rows.length === 0) {
    return [['No zones yet', '0', '0', '0', 'Healthy']]
  }
  return rows.map((row) => [row.zone_id, formatNumber(row.healthy), formatNumber(row.degraded), formatNumber(row.unhealthy), row.status])
}

function formatNumber(value: number) {
  return new Intl.NumberFormat('en-US').format(value)
}

function formatCompactNumber(value: number) {
  return new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 2 }).format(value)
}

function formatSegmentValue(value: number, total: number) {
  const percent = total > 0 ? (value / total) * 100 : 0
  return `${formatNumber(value)} (${percent.toFixed(1)}%)`
}

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


function CompactTable({ rows }: { rows: string[][] }) {
  return (
    <Table>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.join('-')} className="h-9">
            {row.map((cell, index) => (
              <TableCell key={index} className={cn('aurora-table-cell', index === 0 && 'aurora-table-key', index === row.length - 1 && statusClass(cell))}>{cell}</TableCell>
            ))}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function Inventory({ title, columns, rows }: { title: string; columns: string[]; rows: string[][] }) {
  return (
    <Panel title={title} action={<div className="relative w-44"><Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" /><Input className="h-9 pl-9" placeholder="Search..." /></div>}>
      <Table>
        <TableHeader>
          <TableRow>{columns.map((column) => <TableHead key={column} className="aurora-table-head">{column}</TableHead>)}</TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row[0]} className="h-10">
              {row.map((cell, index) => <TableCell key={index} className="aurora-table-cell">{index === row.length - 2 ? <StatusBadge value={cell} /> : cell}</TableCell>)}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Panel>
  )
}

function statusSegmentsFromCounts(rows: Array<{ status: string; count: number }>, total: number) {
  const colors: Record<string, string> = { active: '#22c55e', draining: '#f59e0b', disabled: '#ef4444' }
  const source = rows.length > 0 ? rows : [{ status: 'empty', count: 0 }]
  return source.map((row) => ({ label: statusLabel(row.status), value: formatSegmentValue(row.count, total), color: colors[row.status] ?? '#94a3b8' }))
}

function gatewayShardSegments(rows: Array<{ state: string; count: number }>, total: number) {
  const colors: Record<string, string> = { active: '#22c55e', pending: '#f59e0b', draining: '#ef4444', disabled: '#94a3b8' }
  const source = rows.length > 0 ? rows : [{ state: 'empty', count: 0 }]
  return source.map((row) => ({ label: statusLabel(row.state), value: formatSegmentValue(row.count, total), color: colors[row.state] ?? '#94a3b8' }))
}

function consumerShardRows(rows: Array<{ state: string; count: number; lag: number }>) {
  if (rows.length === 0) return [['No shards yet', '0', '0']]
  return rows.map((row) => [statusLabel(row.state), formatNumber(row.count), formatCompactNumber(row.lag)])
}

function ConsumerOrganizationRows({ rows }: { rows: ConsumerAggregationResponse['organizations'] }) {
  const data = rows ?? []
  if (data.length === 0) return <EmptyState title="No consumer organizations" description="Consumers without tenant_id will appear as No organization when present." />
  return <CompactTable rows={data.map((row) => [row.label || row.tenant_id || 'No organization', formatNumber(row.total), formatNumber(row.active), formatNumber(row.disabled), formatCompactNumber(row.total_lag)])} />
}

function WorkspaceSummary({ row }: { row: ConsumerAggregationResponse['workspace_summary'] }) {
  if (!row) return <EmptyState title="No workspace summary" description="Workspace-scoped consumer totals will appear after data is created." />
  return <CompactTable rows={[[row.workspace_id, formatNumber(row.total_consumers), formatNumber(row.active_consumers), formatNumber(row.disabled_consumers), formatCompactNumber(row.total_lag), formatNumber(row.tenant_count)]]} />
}

function statusLabel(value: string) {
  if (!value) return '-'
  return value.split('_').map((part) => part.charAt(0).toUpperCase() + part.slice(1)).join(' ')
}

function statusClass(value: string) { return value === 'Healthy' ? 'text-emerald-600' : value === 'Degraded' || value === 'Medium' ? 'text-amber-600' : value === 'High' ? 'text-destructive' : '' }
function StatusBadge({ value }: { value: string }) { return <Badge variant="secondary" className={cn('aurora-caption', value === 'Healthy' ? 'bg-emerald-500/10 text-emerald-700' : value === 'Degraded' ? 'bg-amber-500/10 text-amber-700' : 'bg-slate-500/10 text-slate-700')}>{value}</Badge> }

export default function SmtpAdminPage() { return <PageShell /> }
