import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  ChevronDown,
  ChevronLeft,
  RefreshCcw,
  Settings2,
  Trash2,
  Server,
  Database,
  Layers3,
  PackageCheck,
} from 'lucide-react'

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
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { Fetch } from '@/lib/fetch'
import { cn } from '@/lib/utils'
import { PageContent } from '@/components/layout/layout'

// Section imports
import ZoneKpiSection from './sections/ZoneKpiSection'
import ZoneOverviewPanel from './sections/ZoneOverviewPanel'
import ZoneWorkspacesPanel, { type ZoneWorkspace } from './sections/ZoneWorkspacesPanel'
import ZoneServicesPanel, { type ZoneServiceHealth } from './sections/ZoneServicesPanel'
import ZoneInventoryPanel, { type ZoneInventoryMetric } from './sections/ZoneInventoryPanel'
import ZoneActivityPanel, { type ZoneActivity } from './sections/ZoneActivityPanel'

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

function titleCase(value: string) {
  return value
    .replace(/[_-]+/g, ' ')
    .split(' ')
    .filter(Boolean)
    .map((item) => item.charAt(0).toUpperCase() + item.slice(1))
    .join(' ')
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
    const serviceKeys = ['hypervisor', 'storage', 'mail', 'k8s', 'ai']
    const currentEnabled = detail.enabled_services.map((s) => s.key)

    const promises = serviceKeys.map((key) => {
      const isCurrentlyEnabled = currentEnabled.includes(key)
      const shouldBeEnabled = draftServices.includes(key)
      if (isCurrentlyEnabled === shouldBeEnabled) return null

      return Fetch(`/admin/zones/services`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          zone_id: zoneID,
          service_type: key,
          enabled: shouldBeEnabled,
        }),
      }).then(async (response) => {
        if (!response.ok) throw new Error(await readErrorMessage(response))
      })
    }).filter(Boolean) as Promise<void>[]

    if (promises.length === 0) return

    Promise.all(promises)
      .then(async () => {
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
    void Fetch(`/admin/zones/status`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ zone_id: zoneID, status: nextStatus }),
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

        <ZoneKpiSection
          location={detail.zone.location}
          status={detail.zone.status}
          hypervisorsValue={hypervisorMetric ? String(hypervisorMetric.value) : undefined}
          workspacesCount={detail.summary.workspaces}
          enabledServicesCount={detail.summary.enabled_services}
        />

        <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_minmax(420px,0.82fr)]">
          <div className="space-y-6">
            <ZoneOverviewPanel
              zoneName={detail.zone.name}
              zoneCode={detail.zone.code}
              description={detail.zone.description}
              created_at={detail.zone.created_at}
              updated_at={detail.zone.updated_at}
              editingField={editingField}
              draftName={draftName}
              setDraftName={setDraftName}
              draftDescription={draftDescription}
              setDraftDescription={setDraftDescription}
              beginEdit={beginEdit}
              cancelEdit={cancelEdit}
              saveInlineEdit={saveInlineEdit}
            />

            <ZoneWorkspacesPanel
              workspaces={detail.workspaces.items}
              totalCount={detail.workspaces.total}
            />
          </div>

          <div className="space-y-6">
            <ZoneServicesPanel
              enabledServices={detail.enabled_services}
            />

            <ZoneInventoryPanel
              metrics={detail.resource_inventory}
            />

            <ZoneActivityPanel
              activities={detail.recent_activity}
            />
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
