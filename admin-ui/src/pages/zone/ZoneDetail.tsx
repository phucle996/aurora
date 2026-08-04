import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  ChevronDown,
  ChevronLeft,
  RefreshCcw,
  Trash2,
  Server,
  Database,
  Layers3,
  Box,
  Clock,
  Cpu,
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
import { Skeleton } from '@/components/ui/skeleton'
import { Fetch } from '@/lib/fetch'
import { cn } from '@/lib/utils'
import { PageContent } from '@/components/layout/layout'
import { OTPVerificationDialog } from '@/components/zone/OTPVerificationDialog'
import { getOrCreateDeviceKeys, generateNonce, sha256Hex, signPayload } from '@/lib/crypto'

// Section imports
import ZoneWorkspacesPanel, { type ZoneWorkspace } from './sections/ZoneWorkspacesPanel'
import { type ZoneServiceHealth } from './sections/ZoneServicesPanel'
import ZoneInventoryPanel, { type ZoneInventoryMetric } from './sections/ZoneInventoryPanel'
import ZoneActivityPanel, { type ZoneActivity } from './sections/ZoneActivityPanel'
import ZoneEssentialsSection from './sections/ZoneEssentialsSection'
import ZoneServicesSection from './sections/ZoneServicesSection'
import ZoneQuickLinksSection from './sections/ZoneQuickLinksSection'
// [COMMENT]: Import component quản lý khóa mã hóa E2EE X25519 cho Zone
import ZoneEncryptionKeysPanel from './sections/ZoneEncryptionKeysPanel'

// ─── Domain Types ───────────────────────────────────────────────────────────
// Mirrors the API contract from GET /admin/hierarchy/zones/:zone_id

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
  planned: 'Planned',
  active: 'Active',
  draining: 'Draining',
  maintenance: 'Maintenance',
  disabled: 'Disabled',
}

// Mirrors backend state machine in zone_service.go → UpdateZoneStatus().
// Only transitions listed here are permitted; all others are rejected by the API.
const ALLOWED_TRANSITIONS: Record<ZoneStatus, ZoneStatus[]> = {
  planned: ['active', 'disabled'],
  active: ['planned', 'draining'],
  draining: ['active', 'maintenance', 'disabled'],
  maintenance: ['active'],
  disabled: ['planned'],
}



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

function formatDateLong(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('en-US', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    hour12: true
  }).format(date)
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

  // ── Zone data state ──────────────────────────────────────────────────────
  const [detail, setDetail] = useState<ZoneDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

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
  const [otpAction, setOtpAction] = useState<'status' | 'delete' | null>(null)

  // [COMMENT]: Danh sách các Tab điều hướng trong màn hình Chi tiết Zone
  const tabs = ['Overview', 'Workspaces', 'Inventory', 'Encryption Keys', 'Monitoring', 'Activity log', 'Access control', 'Tags']
  const [activeTab, setActiveTab] = useState('Overview')


  // Fetch and normalize zone detail from API; also seeds draft state for editing
  const loadZoneDetail = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const response = await Fetch(`/admin/hierarchy/zones/${encodeURIComponent(zoneID)}`)
      if (!response.ok) throw new Error(await readErrorMessage(response))

      const payload = (await response.json()) as ZoneDetailResponse
      if (!payload.data) throw new Error('Cannot load zone detail')

      const normalizedDetail = normalizeDetail(payload.data)
      setDetail(normalizedDetail)
      // Seed draft state so inline-edit fields start with current values
      setDraftName(normalizedDetail.zone.name)
      setDraftDescription(normalizedDetail.zone.description)
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
  const beginEdit = (field: 'description') => {
    setDraftName(detail.zone.name)
    setDraftDescription(detail.zone.description)
    setEditingField(field)
  }

  // Discard drafts and exit edit mode without saving
  const cancelEdit = () => {
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
    void Fetch(`/admin/hierarchy/zones/${encodeURIComponent(zoneID)}`, {
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

      const bodyString = JSON.stringify({ status: nextStatus })
      const bodyHash = await sha256Hex(bodyString)
      const timestamp = Math.floor(Date.now() / 1000).toString()
      const nonce = generateNonce()
      const path = `/admin/critical/hierarchy/zones/${encodeURIComponent(zoneID)}/status`
      const payloadStr = `PATCH\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`
      const signature = await signPayload(payloadStr, deviceKeys.privateKey)

      const response = await Fetch(path, {
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
      setOtpAction(null)
      await loadZoneDetail()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot update zone status')
    } finally {
      setSigning(false)
    }
  }

  const confirmDeleteZone = async (otpCode: string) => {
    if (!canDeleteZone) return
    setSigning(true)

    try {
      const deviceKeys = await getOrCreateDeviceKeys()
      if (!deviceKeys.privateKey) {
        throw new Error('Security keys are missing on this device. Please log out and sign in again to register your keys.')
      }

      const path = `/admin/critical/hierarchy/zones/${encodeURIComponent(zoneID)}`
      const bodyHash = await sha256Hex("")
      const timestamp = Math.floor(Date.now() / 1000).toString()
      const nonce = generateNonce()
      const payloadStr = `DELETE\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`
      const signature = await signPayload(payloadStr, deviceKeys.privateKey)

      const response = await Fetch(path, {
        method: 'DELETE',
        headers: {
          'X-Admin-Signature': signature,
          'X-Admin-Timestamp': timestamp,
          'X-Admin-Nonce': nonce,
          'X-Admin-StepUp-Code': otpCode,
        },
      })

      if (!response.ok) {
        throw new Error(await readErrorMessage(response))
      }

      setIsOTPOpen(false)
      setOtpAction(null)
      window.location.assign('/zones')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot delete zone')
    } finally {
      setSigning(false)
    }
  }

  const InventoryIcon = ({ metricKey }: { metricKey: string }) => {
    const Icon = metricKey === 'hypervisors'
      ? Server
      : metricKey === 'clusters'
        ? Layers3
        : metricKey === 'vms'
          ? Cpu
          : metricKey === 'storage_accounts'
            ? Database
            : metricKey === 'networks'
              ? Layers3
              : Box
    return <Icon className="size-4 text-muted-foreground/80" />
  }

  const inventoryItems = [
    { key: 'hypervisors', label: 'Hypervisors', value: detail.resource_inventory.find(m => m.key === 'hypervisors')?.value ?? 0 },
    { key: 'clusters', label: 'Clusters', value: detail.resource_inventory.find(m => m.key === 'clusters')?.value ?? 0 },
    { key: 'vms', label: 'Virtual machines', value: detail.resource_inventory.find(m => m.key === 'vms')?.value ?? 0 },
    { key: 'storage_accounts', label: 'Storage accounts', value: detail.resource_inventory.find(m => m.key === 'storage_accounts')?.value ?? 0 },
    { key: 'networks', label: 'Networks', value: detail.resource_inventory.find(m => m.key === 'networks')?.value ?? 0 },
    { key: 'images', label: 'Images', value: detail.resource_inventory.find(m => m.key === 'images')?.value ?? 0 },
  ]

  const renderTabContent = () => {
    switch (activeTab) {
      case 'Overview':
        return (
          <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_380px] items-start">
            {/* Cột trái */}
            <div className="space-y-6">
              {/* Essentials Section Component */}
              <ZoneEssentialsSection
                zoneName={detail.zone.name}
                zoneCode={detail.zone.code}
                location={detail.zone.location}
                status={detail.zone.status}
                description={detail.zone.description}
                created_at={detail.zone.created_at}
                updated_at={detail.zone.updated_at}
                statusDotColor={statusDotColor}
                titleCase={titleCase}
                formatDateLong={formatDateLong}
                formatRelative={formatRelative}
                editingField={editingField}
                draftDescription={draftDescription}
                setDraftDescription={setDraftDescription}
                beginEdit={beginEdit}
                cancelEdit={cancelEdit}
                saveInlineEdit={saveInlineEdit}
              />

              {/* Enabled Services Section Component */}
              <ZoneServicesSection
                zoneID={zoneID}
                enabledServices={detail.enabled_services}
                zoneStatus={detail.zone.status}
                onRefresh={loadZoneDetail}
              />

              {/* Workspaces Block */}
              <div className="border border-border bg-card rounded-lg overflow-hidden shadow-[0_1px_2px_rgba(0,0,0,0.02)] p-5">
                <h3 className="text-sm font-bold text-foreground mb-4">
                  Workspaces in this zone ({detail.workspaces.total})
                </h3>
                {detail.workspaces.items.length === 0 ? (
                  <div className="flex flex-col items-center justify-center border border-dashed border-border rounded-lg p-10 bg-muted/5">
                    <Box className="size-8 text-muted-foreground/50 mb-3" />
                    <p className="text-xs text-muted-foreground mb-1">No workspaces in this zone yet.</p>
                    <button type="button" className="text-xs font-semibold text-primary hover:underline">
                      + Create workspace
                    </button>
                  </div>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full text-xs">
                      <thead>
                        <tr className="border-b border-border text-muted-foreground font-medium text-left">
                          <th className="pb-2.5 font-medium">Workspace</th>
                          <th className="pb-2.5 font-medium">Tenant</th>
                          <th className="pb-2.5 font-medium">Status</th>
                          <th className="pb-2.5 font-medium">Services</th>
                          <th className="pb-2.5 font-medium text-right">Updated</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-border/50">
                        {detail.workspaces.items.map((ws) => (
                          <tr key={ws.id} className="hover:bg-accent/5">
                            <td className="py-3 font-semibold text-primary">{ws.name}</td>
                            <td className="py-3 text-foreground">{ws.tenant_name || '—'}</td>
                            <td className="py-3">
                              <span className={cn(
                                "inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wider",
                                ws.status === 'active' ? "bg-emerald-500/10 text-emerald-600" : "bg-slate-400/10 text-slate-500"
                              )}>
                                {ws.status}
                              </span>
                            </td>
                            <td className="py-3">
                              <div className="flex flex-wrap gap-1">
                                {ws.services.map((svc) => (
                                  <span key={svc} className="inline-flex items-center rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">
                                    {titleCase(svc)}
                                  </span>
                                ))}
                              </div>
                            </td>
                            <td className="py-3 text-right text-muted-foreground">{formatRelative(ws.updated_at)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
                <button
                  type="button"
                  onClick={() => setActiveTab('Workspaces')}
                  className="text-xs font-semibold text-primary hover:underline mt-4 block text-left"
                >
                  View all workspaces
                </button>
              </div>
            </div>

            {/* Cột phải (Sidebar) */}
            <div className="space-y-6">
              {/* Resource Inventory Card */}
              <div className="border border-border bg-card rounded-lg overflow-hidden shadow-[0_1px_2px_rgba(0,0,0,0.02)] p-5">
                <div className="flex items-center justify-between mb-4">
                  <h3 className="text-sm font-bold text-foreground">Resource inventory</h3>
                  <button type="button" onClick={() => setActiveTab('Inventory')} className="text-xs font-semibold text-primary hover:underline">
                    View all
                  </button>
                </div>
                <div className="divide-y divide-border/40 text-xs">
                  {inventoryItems.map((item) => (
                    <div key={item.key} className="flex items-center justify-between py-2.5">
                      <div className="flex items-center gap-2.5">
                        <InventoryIcon metricKey={item.key} />
                        <span className="font-medium text-foreground">{item.label}</span>
                      </div>
                      <span className="font-bold text-foreground">{item.value}</span>
                    </div>
                  ))}
                </div>
              </div>

              {/* Recent Activity Card */}
              <div className="border border-border bg-card rounded-lg overflow-hidden shadow-[0_1px_2px_rgba(0,0,0,0.02)] p-5">
                <div className="flex items-center justify-between mb-4">
                  <h3 className="text-sm font-bold text-foreground">Recent activity</h3>
                  <button type="button" onClick={() => setActiveTab('Activity log')} className="text-xs font-semibold text-primary hover:underline">
                    View all
                  </button>
                </div>

                {detail.recent_activity.length === 0 ? (
                  <div className="flex flex-col items-center justify-center py-8 text-center bg-muted/5 rounded-lg border border-dashed border-border/80">
                    <Clock className="size-8 text-muted-foreground/35 mb-2" />
                    <p className="text-xs text-muted-foreground">No recent activity for this zone.</p>
                  </div>
                ) : (
                  <div className="overflow-x-auto">
                    <table className="w-full text-xs">
                      <thead>
                        <tr className="border-b border-border text-muted-foreground font-medium text-left">
                          <th className="pb-2 font-medium w-1/4">Time</th>
                          <th className="pb-2 font-medium">Activity</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-border/50">
                        {detail.recent_activity.slice(0, 5).map((act) => (
                          <tr key={act.id}>
                            <td className="py-2.5 text-muted-foreground">{formatRelative(act.created_at)}</td>
                            <td className="py-2.5 font-medium text-foreground leading-normal">{act.message}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>

              {/* Quick Links Section Component */}
              <ZoneQuickLinksSection
                onManageServices={() => {
                  document.getElementById('zone-services-section')?.scrollIntoView({ behavior: 'smooth' })
                }}
              />
            </div>
          </div>
        )
      case 'Workspaces':
        return <ZoneWorkspacesPanel workspaces={detail.workspaces.items} totalCount={detail.workspaces.total} />
      case 'Inventory':
        return <ZoneInventoryPanel metrics={detail.resource_inventory} />
      case 'Encryption Keys':
        // [COMMENT]: Hiển thị Panel quản lý Encryption Keys khi chọn Tab Encryption Keys
        return <ZoneEncryptionKeysPanel zoneId={zoneID} />
      case 'Activity log':
        return <ZoneActivityPanel activities={detail.recent_activity} />
      default:
        return (
          <div className="border border-border bg-card rounded-lg p-10 text-center text-sm text-muted-foreground shadow-[0_1px_2px_rgba(0,0,0,0.02)]">
            No data available under {activeTab} for this zone.
          </div>
        )
    }
  }
  return (
    <>
      <PageContent className="pb-8 bg-background">
        {/* Breadcrumb */}
        <nav className="flex items-center gap-1.5 text-xs text-muted-foreground mb-4">
          <Link to="/" className="hover:text-foreground transition-colors">Home</Link>
          <span className="text-muted-foreground/60">&gt;</span>
          <Link to="/zones" className="hover:text-foreground transition-colors">Zones</Link>
          <span className="text-muted-foreground/60">&gt;</span>
          <span className="text-foreground font-medium">{detail.zone.code}</span>
        </nav>

        {/* Title Block */}
        <div className="flex flex-col gap-1.5 mb-4">
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight text-foreground">{detail.zone.name}</h1>
          </div>
          <p className="text-xs text-muted-foreground">{detail.zone.code}</p>
        </div>

        {/* Action Bar */}
        <div className="flex flex-wrap items-center justify-between border-y border-border/80 py-2 mb-4 gap-3 bg-card/30 px-3 rounded-md">
          <div className="flex items-center gap-2">
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button type="button" className="flex items-center gap-1.5  px-2.5 py-1 text-xs font-semibold text-foreground hover:bg-accent/40 focus:outline-none">
                  <span className={cn('size-2 rounded-full', statusDotColor(detail.zone.status))} />
                  <span>{statusLabels[detail.zone.status] ?? titleCase(detail.zone.status || 'unknown')}</span>
                  <ChevronDown className="size-3 text-muted-foreground" />
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="w-48">
                <DropdownMenuLabel>Update status</DropdownMenuLabel>
                <DropdownMenuSeparator />
                {(Object.keys(statusLabels) as ZoneStatus[]).map((status) => {
                  const isCurrent = status === detail.zone.status
                  const isAllowed = ALLOWED_TRANSITIONS[detail.zone.status]?.includes(status) ?? false
                  const isDisabled = isCurrent || !isAllowed
                  return (
                    <DropdownMenuItem
                      key={status}
                      disabled={isDisabled}
                      onSelect={() => requestStatusChange(status)}
                      className="flex items-center justify-between gap-3 text-xs"
                    >
                      <span>{statusLabels[status]}</span>
                      {isCurrent && <span className="text-xs text-muted-foreground">Current</span>}
                    </DropdownMenuItem>
                  )
                })}
              </DropdownMenuContent>
            </DropdownMenu>

            <button
              type="button"
              onClick={() => setDeleteDialogOpen(true)}
              className="flex items-center gap-1.5 bg-card px-2.5 py-1 text-xs font-semibold text-red-300 hover:bg-accent/40 transition-colors"
            >
              <Trash2 className="size-3.5" />
              <span>Delete zone</span>
            </button>
          </div>
        </div>

        {/* Tabs navigation */}
        <div className="flex items-center gap-6 border-b border-border/60 pb-0.5 mb-5 overflow-x-auto scrollbar-none">
          {tabs.map((tab) => {
            const isActive = activeTab === tab
            return (
              <button
                key={tab}
                type="button"
                onClick={() => setActiveTab(tab)}
                className={cn(
                  "text-xs font-semibold transition-all relative pb-2 whitespace-nowrap focus:outline-none",
                  isActive
                    ? "text-foreground font-bold"
                    : "text-muted-foreground hover:text-foreground"
                )}
              >
                {tab}
                {isActive && (
                  <span className="absolute bottom-0 left-0 right-0 h-0.5 bg-primary rounded-full" />
                )}
              </button>
            )
          })}
        </div>

        {/* Tab Content Display */}
        {renderTabContent()}
      </PageContent>



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
              setOtpAction('status')
              setIsOTPOpen(true)
            }}>OK</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      <OTPVerificationDialog
        open={isOTPOpen}
        onOpenChange={(open) => {
          if (signing) return
          setIsOTPOpen(open)
          if (!open) {
            setPendingStatus(null)
            setOtpAction(null)
          }
        }}
        onConfirm={(code) => {
          if (otpAction === 'delete') {
            return confirmDeleteZone(code)
          } else {
            return confirmStatusChange(code)
          }
        }}
        title={otpAction === 'delete' ? "Verify Zone Deletion" : "Security Verification"}
        description={otpAction === 'delete' ? "Deleting this zone is a critical operation. Please enter the 6-digit verification code from your authenticator app to authorize this action." : `Changing zone status to ${pendingStatus ? statusLabels[pendingStatus] : ''} is a critical operation. Please enter the 6-digit verification code from your authenticator app to authorize this action.`}
        confirmText={otpAction === 'delete' ? "Verify & Delete" : "Verify & Update"}
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
            <AlertDialogCancel onClick={() => setOtpAction(null)}>Cancel</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={!canDeleteZone} onClick={() => {
              setDeleteDialogOpen(false)
              setOtpAction('delete')
              setIsOTPOpen(true)
            }}>Delete Zone</AlertDialogAction>
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
