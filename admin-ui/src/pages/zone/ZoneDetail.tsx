import { useCallback, useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { Link } from '@tanstack/react-router'
import {
  Activity,
  Box,
  CalendarClock,
  ChevronDown,
  ChevronLeft,
  Clock3,
  Database,
  Edit3,
  Eye,
  Layers3,
  MapPin,
  PackageCheck,
  RefreshCcw,
  Server,
  Settings2,
  ShieldCheck,
  Trash2,
  Users,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'
import { Skeleton } from '@/components/ui/skeleton'
import { Fetch } from '@/lib/fetch'
import { cn } from '@/lib/utils'
import { PageContent } from '@/components/layout/layout'

type ZoneStatus = 'planned' | 'active' | 'degraded' | 'maintenance' | 'disabled'

type ZoneDetailZone = {
  id: string
  code: string
  name: string
  location: string
  description: string
  status: ZoneStatus
  metadata?: { enabled_services?: string[] } | Record<string, unknown>
  created_at?: string
  updated_at?: string
}

type ZoneSummary = {
  workspaces: number
  enabled_services: number
}

type ZoneServiceHealth = {
  key: string
  label: string
  status: string
  source: string
}

type ZoneInventoryMetric = {
  key: string
  label: string
  value: number
  status: string
  source: string
}

type ZoneWorkspace = {
  id: string
  name: string
  tenant_name: string
  status: string
  services: string[]
  updated_at?: string
}

type ZoneActivity = {
  id: string
  action: string
  target_type: string
  target_id: string
  message: string
  actor_name: string
  created_at?: string
}

type ZoneDetail = {
  zone: ZoneDetailZone
  summary: ZoneSummary
  enabled_services: ZoneServiceHealth[]
  resource_inventory: ZoneInventoryMetric[]
  workspaces: {
    items: ZoneWorkspace[]
    total: number
    limit: number
    offset: number
  }
  recent_activity: ZoneActivity[]
}

type ZoneDetailResponse = {
  message?: string
  error?: string
  data?: ZoneDetail
}

const statusLabels: Record<ZoneStatus, string> = {
  planned: 'Planned',
  active: 'Active',
  degraded: 'Degraded',
  maintenance: 'Maintenance',
  disabled: 'Disabled',
}

const serviceCatalog = [
  { key: 'hypervisor', label: 'Hypervisor', description: 'KVM hosts and compute placement.', icon: Server },
  { key: 'storage', label: 'Storage', description: 'Storage pools and volume capacity.', icon: Database },
  { key: 'kubernetes', label: 'Kubernetes', description: 'Managed clusters and container workloads.', icon: Layers3 },
  { key: 'smtp', label: 'SMTP', description: 'Mail endpoints, gateways, and delivery workers.', icon: PackageCheck },
]

function getZoneIDFromPath() {
  const segments = window.location.pathname.split('/').filter(Boolean)
  return decodeURIComponent(segments[1] ?? '')
}

function normalizeZoneStatus(value: string): ZoneStatus {
  switch (value) {
    case 'active':
    case 'degraded':
    case 'maintenance':
    case 'disabled':
    case 'planned':
      return value
    default:
      return 'planned'
  }
}

function statusTone(status: string) {
  switch (status) {
    case 'active':
    case 'healthy':
      return 'border-emerald-200 bg-emerald-50 text-emerald-700'
    case 'planned':
      return 'border-sky-200 bg-sky-50 text-sky-700'
    case 'degraded':
    case 'warning':
      return 'border-amber-200 bg-amber-50 text-amber-700'
    case 'maintenance':
      return 'border-violet-200 bg-violet-50 text-violet-700'
    case 'disabled':
      return 'border-slate-200 bg-slate-50 text-slate-600'
    default:
      return 'border-border bg-muted/40 text-muted-foreground'
  }
}

function statusDotColor(status: string) {
  switch (status) {
    case 'active':
    case 'healthy':
      return 'bg-emerald-500'
    case 'planned':
      return 'bg-sky-500'
    case 'degraded':
    case 'warning':
      return 'bg-amber-500'
    case 'maintenance':
      return 'bg-violet-500'
    case 'disabled':
      return 'bg-slate-400'
    default:
      return 'bg-muted-foreground'
  }
}

function StatusBadge({ status }: { status: string }) {
  const label = status in statusLabels ? statusLabels[status as ZoneStatus] : titleCase(status || 'unknown')
  return (
    <Badge variant="outline" className={cn('h-7 rounded-lg border px-2.5 text-xs font-semibold', statusTone(status))}>
      {label}
    </Badge>
  )
}

function titleCase(value: string) {
  return value
    .replace(/[_-]+/g, ' ')
    .split(' ')
    .filter(Boolean)
    .map((item) => item.charAt(0).toUpperCase() + item.slice(1))
    .join(' ')
}

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', year: 'numeric' }).format(date)
}

function formatRelative(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  const seconds = Math.max(1, Math.floor((Date.now() - date.getTime()) / 1000))
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

async function readErrorMessage(response: Response) {
  try {
    const payload = (await response.json()) as ZoneDetailResponse
    return payload.message || payload.error || 'Cannot load zone detail'
  } catch {
    return 'Cannot load zone detail'
  }
}

function normalizeDetail(value: ZoneDetail): ZoneDetail {
  return {
    zone: {
      ...value.zone,
      status: normalizeZoneStatus(value.zone.status),
      location: value.zone.location || '—',
      description: value.zone.description || 'No description provided.',
    },
    summary: {
      workspaces: Number(value.summary?.workspaces ?? 0),
      enabled_services: Number(value.summary?.enabled_services ?? 0),
    },
    enabled_services: value.enabled_services ?? [],
    resource_inventory: value.resource_inventory ?? [],
    workspaces: {
      items: value.workspaces?.items ?? [],
      total: Number(value.workspaces?.total ?? 0),
      limit: Number(value.workspaces?.limit ?? 5),
      offset: Number(value.workspaces?.offset ?? 0),
    },
    recent_activity: value.recent_activity ?? [],
  }
}

export default function ZoneDetailPage() {
  const zoneID = useMemo(() => getZoneIDFromPath(), [])
  const [detail, setDetail] = useState<ZoneDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [serviceDrawerOpen, setServiceDrawerOpen] = useState(false)
  const [draftServices, setDraftServices] = useState<string[]>([])
  const [editingField, setEditingField] = useState<'name' | 'description' | null>(null)
  const [draftName, setDraftName] = useState('')
  const [draftDescription, setDraftDescription] = useState('')
  const [pendingStatus, setPendingStatus] = useState<ZoneStatus | null>(null)
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false)

  const loadZoneDetail = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const response = await Fetch(`/admin/zones/${encodeURIComponent(zoneID)}`)
      if (!response.ok) {
        throw new Error(await readErrorMessage(response))
      }
      const payload = (await response.json()) as ZoneDetailResponse
      if (!payload.data) {
        throw new Error('Cannot load zone detail')
      }
      const normalizedDetail = normalizeDetail(payload.data)
      setDetail(normalizedDetail)
      setDraftName(normalizedDetail.zone.name)
      setDraftDescription(normalizedDetail.zone.description)
      setDraftServices(normalizedDetail.enabled_services.map((service) => service.key))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot load zone detail')
      setDetail(null)
    } finally {
      setLoading(false)
    }
  }, [zoneID])

  useEffect(() => {
    void Promise.resolve().then(loadZoneDetail)
  }, [loadZoneDetail])

  if (loading) {
    return <ZoneDetailSkeleton />
  }

  if (error || !detail) {
    return (
      <PageContent className="pb-0">
        <Button asChild variant="ghost" className="px-0 text-primary hover:bg-transparent">
          <Link to="/zones">
            <ChevronLeft className="size-4" />
            Back to zones
          </Link>
        </Button>
        <div className="rounded-xl border border-border bg-card p-8 text-center shadow-xs">
          <p className="text-lg font-semibold text-foreground">{error === 'zone not found' ? 'Zone not found' : 'Cannot load zone detail'}</p>
          <p className="mt-2 text-sm text-muted-foreground">{error || 'The selected zone could not be loaded.'}</p>
          <Button type="button" className="mt-5" onClick={() => void loadZoneDetail()}>
            <RefreshCcw className="size-4" />
            Retry
          </Button>
        </div>
      </PageContent>
    )
  }

  const hypervisorMetric = detail.resource_inventory.find((item) => item.key === 'hypervisors')

  const deleteBlockers = [
    detail.workspaces.total > 0 ? `${detail.workspaces.total} workspace${detail.workspaces.total === 1 ? '' : 's'}` : '',
    detail.enabled_services.length > 0 ? `${detail.enabled_services.length} enabled service${detail.enabled_services.length === 1 ? '' : 's'}` : '',
    ...detail.resource_inventory
      .filter((metric) => Number(metric.value) > 0)
      .map((metric) => `${metric.value} ${metric.label}`),
  ].filter(Boolean)
  const canDeleteZone = deleteBlockers.length === 0

  const beginEdit = (field: 'name' | 'description') => {
    setDraftName(detail.zone.name)
    setDraftDescription(detail.zone.description)
    setEditingField(field)
  }

  const cancelEdit = () => {
    setDraftName(detail.zone.name)
    setDraftDescription(detail.zone.description)
    setEditingField(null)
  }

  const saveInlineEdit = () => {
    const nextName = draftName.trim()
    const nextDescription = draftDescription.trim()
    setEditingField(null)
    const body: Record<string, string> = {}
    if (nextName && nextName !== detail.zone.name) body.name = nextName
    if (nextDescription !== detail.zone.description) body.description = nextDescription || ''
    if (Object.keys(body).length === 0) return
    void Fetch(`/admin/zones/${encodeURIComponent(zoneID)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readErrorMessage(response))
        await loadZoneDetail()
      })
      .catch((err) => setError(err instanceof Error ? err.message : 'Cannot update zone'))
  }

  const toggleDraftService = (serviceKey: string) => {
    setDraftServices((current) => (
      current.includes(serviceKey)
        ? current.filter((key) => key !== serviceKey)
        : [...current, serviceKey]
    ))
  }

  const applyServices = () => {
    setServiceDrawerOpen(false)
    void Fetch(`/admin/zones/${encodeURIComponent(zoneID)}/services`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled_services: draftServices }),
    })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readErrorMessage(response))
        await loadZoneDetail()
      })
      .catch((err) => setError(err instanceof Error ? err.message : 'Cannot update zone services'))
  }

  const requestStatusChange = (status: ZoneStatus) => {
    if (status === detail.zone.status) return
    setPendingStatus(status)
  }

  const confirmStatusChange = () => {
    if (!pendingStatus) return
    const nextStatus = pendingStatus
    setPendingStatus(null)
    void Fetch(`/admin/zones/${encodeURIComponent(zoneID)}/status`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ status: nextStatus }),
    })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readErrorMessage(response))
        await loadZoneDetail()
      })
      .catch((err) => setError(err instanceof Error ? err.message : 'Cannot update zone status'))
  }

  const confirmDeleteZone = () => {
    if (!canDeleteZone) return
    setDeleteDialogOpen(false)
    void Fetch(`/admin/zones/${encodeURIComponent(zoneID)}`, { method: 'DELETE' })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readErrorMessage(response))
        window.location.assign('/zones')
      })
      .catch((err) => setError(err instanceof Error ? err.message : 'Cannot delete zone'))
  }

  return (
    <>
      <PageContent className="pb-0">
      <div className="flex flex-col gap-5 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-3">
          <nav className="flex items-center gap-2 text-sm font-semibold text-muted-foreground">
            <Link to="/zones" className="text-primary hover:underline">Zone</Link>
            <span>/</span>
            <span className="text-foreground">{detail.zone.code}</span>
          </nav>
          <div>
            <h1 className="text-3xl font-semibold tracking-[-0.03em] text-foreground md:text-4xl">{detail.zone.code}</h1>
            <p className="mt-2 text-sm text-muted-foreground md:text-base">View resources, services, and workspaces inside this zone.</p>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-3 lg:pt-10">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" className="h-11 gap-2 rounded-lg px-4 text-sm font-semibold shadow-sm">
                <span className={cn('size-2.5 rounded-full', statusDotColor(detail.zone.status))} />
                <span>{statusLabels[detail.zone.status] ?? titleCase(detail.zone.status || 'unknown')}</span>
                <ChevronDown className="size-4 text-muted-foreground" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-56">
              <DropdownMenuLabel>Update status</DropdownMenuLabel>
              <DropdownMenuSeparator />
              {(Object.keys(statusLabels) as ZoneStatus[]).map((status) => (
                <DropdownMenuItem
                  key={status}
                  disabled={status === detail.zone.status}
                  onSelect={() => requestStatusChange(status)}
                  className="flex items-center justify-between gap-3"
                >
                  <span>{statusLabels[status]}</span>
                  {status === detail.zone.status && <span className="text-xs text-muted-foreground">Current</span>}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
          <Button variant="destructive" className="h-11 rounded-lg px-4 text-sm font-semibold shadow-sm" onClick={() => setDeleteDialogOpen(true)}>
            <Trash2 className="size-4" />
            Delete Zone
          </Button>
          <Button className="h-11 rounded-lg px-6 text-sm font-semibold shadow-sm" onClick={() => setServiceDrawerOpen(true)}>
            <Settings2 className="size-4" />
            Manage Services
          </Button>
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-5">
        <KpiCard icon={MapPin} label="Location" value={detail.zone.location} className="md:col-span-2" />
        <KpiCard icon={ShieldCheck} label="Status" value={<StatusBadge status={detail.zone.status} />} />
        {hypervisorMetric && <KpiCard icon={Server} label={hypervisorMetric.label} value={String(hypervisorMetric.value)} />}
        <KpiCard icon={Users} label="Workspaces" value={String(detail.summary.workspaces)} />
        <KpiCard icon={PackageCheck} label="Enabled Services" value={String(detail.summary.enabled_services)} />
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(420px,0.82fr)]">
        <div className="space-y-6">
          <Panel title="Zone Overview" icon={Eye}>
            <div className="grid gap-4 text-sm sm:grid-cols-[180px_1fr]">
              <OverviewLabel>Zone Name</OverviewLabel>
              <EditableOverviewValue
                editing={editingField === 'name'}
                onEdit={() => beginEdit('name')}
                onCancel={cancelEdit}
                onSave={saveInlineEdit}
              >
                {editingField === 'name' ? (
                  <Input value={draftName} onChange={(event) => setDraftName(event.target.value)} className="h-10" autoFocus />
                ) : detail.zone.name}
              </EditableOverviewValue>
              <OverviewLabel>Zone Code</OverviewLabel><OverviewValue>{detail.zone.code}</OverviewValue>
              <OverviewLabel>Description</OverviewLabel>
              <EditableOverviewValue
                editing={editingField === 'description'}
                onEdit={() => beginEdit('description')}
                onCancel={cancelEdit}
                onSave={saveInlineEdit}
              >
                {editingField === 'description' ? (
                  <Textarea value={draftDescription} onChange={(event) => setDraftDescription(event.target.value)} className="min-h-24" autoFocus />
                ) : detail.zone.description}
              </EditableOverviewValue>
              <OverviewLabel>Created By</OverviewLabel><OverviewValue>System Admin</OverviewValue>
              <OverviewLabel>Created At</OverviewLabel><OverviewValue>{formatDate(detail.zone.created_at)}</OverviewValue>
              <OverviewLabel>Last Updated</OverviewLabel><OverviewValue>{formatRelative(detail.zone.updated_at)}</OverviewValue>
            </div>
          </Panel>

          <Panel title="Workspaces in this Zone" icon={Users}>
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Workspace</TableHead>
                  <TableHead>Tenant</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Services</TableHead>
                  <TableHead className="text-right">Updated</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {detail.workspaces.items.length === 0 && (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={5} className="h-28 text-center text-sm text-muted-foreground">No workspaces in this zone yet.</TableCell>
                  </TableRow>
                )}
                {detail.workspaces.items.map((workspace) => (
                  <TableRow key={workspace.id}>
                    <TableCell className="font-semibold text-primary">{workspace.name}</TableCell>
                    <TableCell>{workspace.tenant_name || '—'}</TableCell>
                    <TableCell><StatusBadge status={workspace.status} /></TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-2">
                        {workspace.services.length === 0 && <span className="text-sm text-muted-foreground">—</span>}
                        {workspace.services.map((service) => (
                          <Badge key={service} variant="outline" className="rounded-md text-xs">{titleCase(service)}</Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="text-right text-muted-foreground">{formatRelative(workspace.updated_at)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <p className="mt-4 text-sm text-muted-foreground">Showing {detail.workspaces.items.length} of {detail.workspaces.total} workspaces</p>
          </Panel>
        </div>

        <div className="space-y-6">
          <Panel title="Enabled Services" icon={PackageCheck}>
            <div className="grid gap-4 sm:grid-cols-2">
              {detail.enabled_services.length === 0 && <EmptyPanelText>No enabled services configured yet.</EmptyPanelText>}
              {detail.enabled_services.map((service) => (
                <div key={service.key} className="flex h-12 items-center justify-between rounded-lg border border-border bg-background px-4 shadow-xs transition-colors hover:border-primary/25 hover:bg-primary/3">
                  <div className="flex items-center gap-3">
                    <ServiceIcon serviceKey={service.key} />
                    <p className="text-sm font-medium text-primary">{service.label || titleCase(service.key)}</p>
                  </div>
                  <div className="flex shrink-0 items-center gap-2 text-sm font-medium">
                    <span className={cn('size-2 rounded-full', serviceStatusDotTone(service.status))} />
                    <span className={serviceStatusTextTone(service.status)}>{serviceStatusLabel(service.status)}</span>
                  </div>
                </div>
              ))}
            </div>
          </Panel>

          <Panel title="Resource Inventory" icon={Layers3}>
            {detail.resource_inventory.length === 0 ? (
              <EmptyPanelText>No inventory sources connected yet.</EmptyPanelText>
            ) : (
              <div className="grid gap-x-10 gap-y-3 sm:grid-cols-2">
                {splitInventoryColumns(detail.resource_inventory).map((column, columnIndex) => (
                  <div key={columnIndex} className={cn('space-y-3', columnIndex === 1 && 'sm:border-l sm:border-border sm:pl-10')}>
                    {column.map((metric) => (
                      <div key={`${metric.source}-${metric.key}`} className="flex h-8 items-center justify-between gap-6">
                        <div className="flex min-w-0 items-center gap-3">
                          <InventoryIcon metricKey={metric.key} />
                          <p className="truncate text-sm font-medium text-primary">{metric.label}</p>
                        </div>
                        <p className="shrink-0 text-sm font-semibold text-primary">{metric.value}</p>
                      </div>
                    ))}
                  </div>
                ))}
              </div>
            )}
          </Panel>

          <Panel title="Recent Activity" icon={Activity}>
            {detail.recent_activity.length === 0 ? (
              <EmptyPanelText>No recent activity for this zone yet.</EmptyPanelText>
            ) : (
              <div className="divide-y divide-border">
                {detail.recent_activity.map((activity) => (
                  <div key={activity.id} className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
                    <CalendarClock className="mt-0.5 size-4 shrink-0 text-primary" />
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-medium text-foreground">{activity.message}</p>
                      <p className="mt-1 text-xs text-muted-foreground">{titleCase(activity.action)} · {activity.actor_name || 'System'}</p>
                    </div>
                    <span className="shrink-0 text-xs text-muted-foreground">{formatRelative(activity.created_at)}</span>
                  </div>
                ))}
              </div>
            )}
          </Panel>
        </div>
      </div>
    </PageContent>

      <Sheet open={serviceDrawerOpen} onOpenChange={setServiceDrawerOpen}>
        <SheetContent side="right" className="w-full sm:max-w-md">
          <SheetHeader>
            <SheetTitle>Manage Zone Services</SheetTitle>
            <SheetDescription>
              Choose which services are enabled for this zone. Service health still comes from each service owner when available.
            </SheetDescription>
          </SheetHeader>

          <div className="grid gap-3 px-4 py-2">
            {serviceCatalog.map((service) => {
              const Icon = service.icon
              const checked = draftServices.includes(service.key)
              return (
                <div key={service.key} className="flex items-center justify-between gap-4 rounded-xl border border-border bg-background p-4">
                  <div className="flex min-w-0 items-start gap-3">
                    <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
                      <Icon className="size-4" />
                    </div>
                    <div className="min-w-0">
                      <Label className="text-sm font-semibold text-foreground">{service.label}</Label>
                      <p className="mt-1 text-xs leading-5 text-muted-foreground">{service.description}</p>
                    </div>
                  </div>
                  <Switch checked={checked} onCheckedChange={() => toggleDraftService(service.key)} />
                </div>
              )
            })}
          </div>

          <SheetFooter>
            <Button type="button" variant="outline" onClick={() => setServiceDrawerOpen(false)}>Cancel</Button>
            <Button type="button" onClick={applyServices}>Apply Services</Button>
          </SheetFooter>
        </SheetContent>
      </Sheet>

      <AlertDialog open={pendingStatus !== null} onOpenChange={(open) => !open && setPendingStatus(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Confirm status update</AlertDialogTitle>
            <AlertDialogDescription>
              Update zone status from {statusLabels[detail.zone.status]} to {pendingStatus ? statusLabels[pendingStatus] : 'selected status'}?
              This change will be saved to topology manager and recorded in recent activity.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={confirmStatusChange}>OK</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <AlertDialog open={deleteDialogOpen} onOpenChange={setDeleteDialogOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete this zone?</AlertDialogTitle>
            <AlertDialogDescription>
              {canDeleteZone
                ? `This will permanently delete ${detail.zone.code}. This action cannot be undone.`
                : `This zone cannot be deleted until you remove: ${deleteBlockers.join(', ')}.`}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={!canDeleteZone} onClick={confirmDeleteZone}>Delete Zone</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

function ZoneDetailSkeleton() {
  return (
    <PageContent className="pb-0">
      <Skeleton className="h-24 rounded-xl" />
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-5">
        {Array.from({ length: 5 }).map((_, index) => <Skeleton key={index} className="h-28 rounded-xl border border-border" />)}
      </div>
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(420px,0.82fr)]">
        <Skeleton className="h-96 rounded-xl border border-border" />
        <Skeleton className="h-96 rounded-xl border border-border" />
      </div>
    </PageContent>
  )
}

function KpiCard({ icon: Icon, label, value, className }: { icon: LucideIcon; label: string; value: ReactNode; className?: string }) {
  return (
    <div className={cn("flex items-center gap-4 rounded-xl border border-border bg-card p-5 shadow-xs", className)}>
      <div className="flex size-12 shrink-0 items-center justify-center rounded-xl border border-primary/20 bg-primary/10 text-primary">
        <Icon className="size-5" />
      </div>
      <div className="min-w-0">
        <p className="text-sm font-medium text-muted-foreground">{label}</p>
        <div className="mt-1 truncate text-lg font-semibold text-foreground">{value}</div>
      </div>
    </div>
  )
}

function Panel({ title, icon: Icon, children }: { title: string; icon: LucideIcon; children: ReactNode }) {
  return (
    <section className="rounded-xl border border-border bg-card p-6 shadow-xs">
      <div className="mb-5 flex items-center justify-between">
        <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">{title}</h2>
        <Icon className="size-5 text-primary" />
      </div>
      {children}
    </section>
  )
}

function OverviewLabel({ children }: { children: ReactNode }) {
  return <p className="font-medium text-muted-foreground">{children}</p>
}

function OverviewValue({ children }: { children: ReactNode }) {
  return <p className="font-medium leading-6 text-foreground">{children}</p>
}

function EditableOverviewValue({
  children,
  editing,
  onCancel,
  onEdit,
  onSave,
}: {
  children: ReactNode
  editing: boolean
  onCancel: () => void
  onEdit: () => void
  onSave: () => void
}) {
  return (
    <div className="group min-w-0">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1 font-medium leading-6 text-foreground">{children}</div>
        {!editing && (
          <button
            type="button"
            onClick={onEdit}
            className="mt-0.5 inline-flex size-7 shrink-0 items-center justify-center rounded-md text-muted-foreground opacity-0 transition-all hover:bg-accent hover:text-foreground group-hover:opacity-100 focus-visible:opacity-100"
            aria-label="Edit field"
          >
            <Edit3 className="size-3.5" />
          </button>
        )}
      </div>
      {editing && (
        <div className="mt-3 flex justify-end gap-2">
          <Button type="button" size="sm" variant="outline" onClick={onCancel}>Cancel</Button>
          <Button type="button" size="sm" onClick={onSave}>Save</Button>
        </div>
      )}
    </div>
  )
}

function EmptyPanelText({ children }: { children: ReactNode }) {
  return <div className="rounded-lg border border-dashed border-border bg-muted/20 p-6 text-center text-sm font-medium text-muted-foreground">{children}</div>
}

function ServiceIcon({ serviceKey }: { serviceKey: string }) {
  const Icon = serviceKey === 'hypervisor'
    ? Server
    : serviceKey === 'storage'
      ? Database
      : serviceKey === 'kubernetes'
        ? Layers3
        : serviceKey === 'smtp'
          ? PackageCheck
          : Clock3
  return <Icon className="size-4" />
}

function InventoryIcon({ metricKey }: { metricKey: string }) {
  const Icon = metricKey.includes('hypervisor')
    ? Server
    : metricKey.includes('smtp')
      ? PackageCheck
      : metricKey.includes('storage') || metricKey.includes('database')
        ? Database
        : metricKey.includes('kubernetes') || metricKey.includes('network')
          ? Layers3
          : Box
  return <Icon className="size-4" />
}

function serviceStatusLabel(status?: string) {
  switch ((status || '').toLowerCase()) {
    case 'healthy':
    case 'active':
      return 'Healthy'
    case 'degraded':
    case 'warning':
      return 'Degraded'
    case 'disabled':
      return 'Disabled'
    default:
      return 'Unknown'
  }
}

function serviceStatusDotTone(status?: string) {
  switch ((status || '').toLowerCase()) {
    case 'healthy':
    case 'active':
      return 'bg-emerald-500'
    case 'degraded':
    case 'warning':
      return 'bg-amber-500'
    case 'disabled':
      return 'bg-slate-400'
    default:
      return 'bg-slate-300'
  }
}

function serviceStatusTextTone(status?: string) {
  switch ((status || '').toLowerCase()) {
    case 'healthy':
    case 'active':
      return 'text-emerald-600'
    case 'degraded':
    case 'warning':
      return 'text-amber-600'
    case 'disabled':
      return 'text-slate-500'
    default:
      return 'text-muted-foreground'
  }
}

function splitInventoryColumns<T>(items: T[]): T[][] {
  const half = Math.ceil(items.length / 2)
  const left = items.slice(0, half)
  const right = items.slice(half)
  return right.length > 0 ? [left, right] : [left]
}
