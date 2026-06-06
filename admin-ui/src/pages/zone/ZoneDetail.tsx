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
import { OTPVerificationDialog } from '@/components/zone/OTPVerificationDialog'
import { getOrCreateDeviceKeys, generateNonce, sha256Hex, signPayload } from '@/lib/crypto'

// Section imports
import ZoneKpiSection from './sections/ZoneKpiSection'
import ZoneOverviewPanel from './sections/ZoneOverviewPanel'
import ZoneWorkspacesPanel, { type ZoneWorkspace } from './sections/ZoneWorkspacesPanel'
import ZoneServicesPanel, { type ZoneServiceHealth } from './sections/ZoneServicesPanel'
import ZoneInventoryPanel, { type ZoneInventoryMetric } from './sections/ZoneInventoryPanel'
import ZoneActivityPanel, { type ZoneActivity } from './sections/ZoneActivityPanel'

// ─── Domain Types ───────────────────────────────────────────────────────────
// Mirrors the API contract from GET /admin/core/zones/:zone_id

type ZoneStatus = 'planned' | 'active' | 'draining' | 'maintenance' | 'disabled'

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

// API response envelope — data is null on error
type ZoneDetailResponse = {
  message?: string
  error?: string
  data?: ZoneDetail
}

// ─── Display Constants ───────────────────────────────────────────────────────

// Human-readable labels for each zone lifecycle status
const statusLabels: Record<ZoneStatus, string> = {
  planned:     'Planned',
  active:      'Active',
  draining:    'Draining',
  maintenance: 'Maintenance',
  disabled:    'Disabled',
}

// Mirrors backend state machine in zone_service.go → UpdateZoneStatus().
// Only transitions listed here are permitted; all others are rejected by the API.
const ALLOWED_TRANSITIONS: Record<ZoneStatus, ZoneStatus[]> = {
  planned:     ['active', 'disabled'],
  active:      ['draining', 'maintenance', 'disabled'],
  draining:    ['active', 'maintenance', 'disabled'],
  maintenance: ['active', 'disabled'],
  disabled:    ['active'],
}

// Static catalog of zone services shown in the Manage Services drawer
const serviceCatalog = [
  { key: 'hypervisor', label: 'Hypervisor', description: 'KVM hosts and compute placement.', icon: Server },
  { key: 'storage', label: 'Storage', description: 'Storage pools and volume capacity.', icon: Database },
  { key: 'kubernetes', label: 'Kubernetes', description: 'Managed clusters and container workloads.', icon: Layers3 },
  { key: 'mail', label: 'Mail', description: 'Mail endpoints, gateways, and delivery workers.', icon: PackageCheck },
]

// ─── Utility Functions ───────────────────────────────────────────────────────

// Extract zone UUID from URL path: /zones/<zone_id>
function getZoneIDFromPath() {
  const segments = window.location.pathname.split('/').filter(Boolean)
  return decodeURIComponent(segments[1] ?? '')
}

// Coerce any unknown status string to a valid ZoneStatus; defaults to 'planned'
function normalizeZoneStatus(value: string): ZoneStatus {
  switch (value) {
    case 'active':
    case 'draining':
    case 'maintenance':
    case 'disabled':
    case 'planned':
      return value
    default:
      return 'planned'
  }
}

// Returns a Tailwind bg-color class for the status indicator dot
function statusDotColor(status: string) {
  switch (status) {
    case 'active':
    case 'healthy':
      return 'bg-emerald-500'
    case 'planned':
      return 'bg-sky-500'
    case 'draining':
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

// Safely extract error message from a non-ok API response
async function readErrorMessage(response: Response) {
  try {
    const payload = (await response.json()) as ZoneDetailResponse
    return payload.message || payload.error || 'Cannot load zone detail'
  } catch {
    return 'Cannot load zone detail'
  }
}

// Normalize raw API data: coerce types, fill missing fields with safe defaults
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
  // Zone ID is stable for the lifetime of this page
  const zoneID = useMemo(() => getZoneIDFromPath(), [])

  // ── Core data state ──────────────────────────────────────────────────────
  const [detail, setDetail] = useState<ZoneDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  // ── Manage Services drawer ────────────────────────────────────────────────
  const [serviceDrawerOpen, setServiceDrawerOpen] = useState(false)
  const [draftServices, setDraftServices] = useState<string[]>([]) // local draft, not yet saved

  // ── Inline edit (name / description) ─────────────────────────────────────
  const [editingField, setEditingField] = useState<'name' | 'description' | null>(null)
  const [draftName, setDraftName] = useState('')
  const [draftDescription, setDraftDescription] = useState('')

  // ── Status change flow: confirm → OTP → API ───────────────────────────────
  const [pendingStatus, setPendingStatus] = useState<ZoneStatus | null>(null) // status awaiting confirmation
  const [isStatusConfirmOpen, setIsStatusConfirmOpen] = useState(false)       // step 1: confirm dialog
  const [isOTPOpen, setIsOTPOpen] = useState(false)                           // step 2: OTP dialog
  const [signing, setSigning] = useState(false)                               // true while API call in-flight

  // ── Delete zone dialog ────────────────────────────────────────────────────
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false)

  // Fetch and normalize zone detail from API; also seeds draft state for editing
  const loadZoneDetail = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const response = await Fetch(`/admin/core/zones/${encodeURIComponent(zoneID)}`)
      if (!response.ok) throw new Error(await readErrorMessage(response))

      const payload = (await response.json()) as ZoneDetailResponse
      if (!payload.data) throw new Error('Cannot load zone detail')

      const normalizedDetail = normalizeDetail(payload.data)
      setDetail(normalizedDetail)
      // Seed draft state so inline-edit fields start with current values
      setDraftName(normalizedDetail.zone.name)
      setDraftDescription(normalizedDetail.zone.description)
      setDraftServices(normalizedDetail.enabled_services.filter((service) => service.status === 'healthy').map((service) => service.key))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot load zone detail')
      setDetail(null)
    } finally {
      setLoading(false)
    }
  }, [zoneID])

  // Load on mount; void wrapper suppresses unhandled-promise lint warning
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

  const activeEnabledServices = detail.enabled_services.filter((s) => s.status === 'healthy')
  const deleteBlockers = [
    detail.workspaces.total > 0 ? `${detail.workspaces.total} workspace${detail.workspaces.total === 1 ? '' : 's'}` : '',
    activeEnabledServices.length > 0 ? `${activeEnabledServices.length} enabled service${activeEnabledServices.length === 1 ? '' : 's'}` : '',
    ...detail.resource_inventory
      .filter((metric) => Number(metric.value) > 0)
      .map((metric) => `${metric.value} ${metric.label}`),
  ].filter(Boolean)
  const canDeleteZone = deleteBlockers.length === 0

  // ── Inline edit handlers ─────────────────────────────────────────────────

  // Reset drafts to current values before entering edit mode
  const beginEdit = (field: 'name' | 'description') => {
    setDraftName(detail.zone.name)
    setDraftDescription(detail.zone.description)
    setEditingField(field)
  }

  // Discard drafts and exit edit mode without saving
  const cancelEdit = () => {
    setDraftName(detail.zone.name)
    setDraftDescription(detail.zone.description)
    setEditingField(null)
  }

  // PATCH only changed fields; no-ops if nothing changed
  const saveInlineEdit = () => {
    const nextName = draftName.trim()
    const nextDescription = draftDescription.trim()
    setEditingField(null)
    const body: Record<string, string> = {}
    if (nextName && nextName !== detail.zone.name) body.name = nextName
    if (nextDescription !== detail.zone.description) body.description = nextDescription || ''
    if (Object.keys(body).length === 0) return // nothing changed
    void Fetch(`/admin/core/zones/${encodeURIComponent(zoneID)}`, {
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

  // ── Manage Services handlers ─────────────────────────────────────────────

  // Toggle a service key in local draft (not yet persisted)
  const toggleDraftService = (serviceKey: string) => {
    setDraftServices((current) => (
      current.includes(serviceKey)
        ? current.filter((key) => key !== serviceKey)
        : [...current, serviceKey]
    ))
  }

  // Fire PUT for each service whose enabled state changed; reload on success
  const applyServices = () => {
    setServiceDrawerOpen(false)
    const currentEnabled = detail.enabled_services.filter((s) => s.status === 'healthy').map((s) => s.key)

    const promises = serviceCatalog.map((service) => {
      const isCurrentlyEnabled = currentEnabled.includes(service.key)
      const shouldBeEnabled = draftServices.includes(service.key)
      if (isCurrentlyEnabled === shouldBeEnabled) return null // no change

      return Fetch(`/admin/core/zones/services`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ zone_id: zoneID, service_type: service.key, enabled: shouldBeEnabled }),
      }).then(async (response) => {
        if (!response.ok) throw new Error(await readErrorMessage(response))
      })
    }).filter(Boolean) as Promise<void>[]

    if (promises.length === 0) return

    Promise.all(promises)
      .then(async () => { await loadZoneDetail() })
      .catch((err) => setError(err instanceof Error ? err.message : 'Cannot update zone services'))
  }

  // ── Status change flow ───────────────────────────────────────────────────

  // Step 1: user picks a new status → open confirmation dialog
  const requestStatusChange = (status: ZoneStatus) => {
    if (!detail) return
    if (status === detail.zone.status) return // already current status
    setPendingStatus(status)
    setIsStatusConfirmOpen(true)
  }

  const confirmStatusChange = async (otpCode: string) => {
    // Capture pendingStatus immediately at call time to avoid stale closure
    // issues caused by onOpenChange resetting it during async flow
    const nextStatus = pendingStatus
    if (!nextStatus) return
    setSigning(true)

    try {
      const deviceKeys = await getOrCreateDeviceKeys()
      if (!deviceKeys.privateKey) {
        throw new Error('Security keys are missing on this device. Please log out and sign in again to register your keys.')
      }

      const bodyString = JSON.stringify({ zone_id: zoneID, status: nextStatus })
      const bodyHash = await sha256Hex(bodyString)
      const timestamp = Math.floor(Date.now() / 1000).toString()
      const nonce = generateNonce()
      const payloadStr = `PATCH\n/admin/core/zones/status\n\n${bodyHash}\n${timestamp}\n${nonce}`
      const signature = await signPayload(payloadStr, deviceKeys.privateKey)

      const response = await Fetch(`/admin/core/zones/status`, {
        method: 'PATCH',
        headers: {
          'Content-Type': 'application/json',
          'X-Admin-Signature': signature,
          'X-Admin-Timestamp': timestamp,
          'X-Admin-Nonce': nonce,
          'X-Admin-StepUp-Code': otpCode,
        },
        body: bodyString,
      })

      if (!response.ok) {
        throw new Error(await readErrorMessage(response))
      }

      // Close dialog and clear state only after successful API call
      setIsOTPOpen(false)
      setPendingStatus(null)
      await loadZoneDetail()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot update zone status')
    } finally {
      setSigning(false)
    }
  }

  const confirmDeleteZone = () => {
    if (!canDeleteZone) return
    setDeleteDialogOpen(false)
    void Fetch(`/admin/core/zones/${encodeURIComponent(zoneID)}`, { method: 'DELETE' })
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
                {(Object.keys(statusLabels) as ZoneStatus[]).map((status) => {
                  const isCurrent = status === detail.zone.status
                  // A transition is valid if it appears in ALLOWED_TRANSITIONS for the current status
                  const isAllowed = ALLOWED_TRANSITIONS[detail.zone.status]?.includes(status) ?? false
                  const isDisabled = isCurrent || !isAllowed
                  return (
                    <DropdownMenuItem
                      key={status}
                      disabled={isDisabled}
                      onSelect={() => requestStatusChange(status)}
                      className="flex items-center justify-between gap-3"
                    >
                      <span>{statusLabels[status]}</span>
                      {isCurrent && <span className="text-xs text-muted-foreground">Current</span>}
                    </DropdownMenuItem>
                  )
                })}
              </DropdownMenuContent>
            </DropdownMenu>
            <Button variant="destructive" className="h-11 rounded-lg px-4 text-sm font-semibold shadow-sm" onClick={() => setDeleteDialogOpen(true)}>
              <Trash2 className="size-4" />
              Delete Zone
            </Button>
            {/* Only allowed in maintenance mode — backend enforces the same constraint */}
            <Button
              className="h-11 rounded-lg px-6 text-sm font-semibold shadow-sm"
              disabled={detail.zone.status !== 'maintenance'}
              title={detail.zone.status !== 'maintenance' ? 'Zone must be in Maintenance status to manage services' : undefined}
              onClick={() => setServiceDrawerOpen(true)}
            >
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
              enabledServices={detail.enabled_services.filter((s) => s.status === 'healthy')}
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

      <AlertDialog open={isStatusConfirmOpen} onOpenChange={setIsStatusConfirmOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Confirm status update</AlertDialogTitle>
            <AlertDialogDescription>
              Update zone status from {detail ? statusLabels[detail.zone.status] : ''} to {pendingStatus ? statusLabels[pendingStatus] : 'selected status'}?
              This change will be saved to topology manager and recorded in recent activity.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel onClick={() => {
              setIsStatusConfirmOpen(false)
              setPendingStatus(null)
            }}>Cancel</AlertDialogCancel>
            <AlertDialogAction onClick={() => {
              setIsStatusConfirmOpen(false)
              setIsOTPOpen(true)
            }}>OK</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <OTPVerificationDialog
        open={isOTPOpen}
        onOpenChange={(open) => {
          // Do not allow closing the dialog while signing is in progress
          // to prevent clearing pendingStatus before the API call completes
          if (signing) return
          setIsOTPOpen(open)
          if (!open) setPendingStatus(null)
        }}
        onConfirm={confirmStatusChange}
        title="Security Verification"
        description={`Changing zone status to ${pendingStatus ? statusLabels[pendingStatus] : ''} is a critical operation. Please enter the 6-digit verification code from your authenticator app to authorize this action.`}
        confirmText="Verify & Update"
        loading={signing}
      />

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
