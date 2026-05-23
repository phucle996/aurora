import { useMemo, useState, type ElementType, type ReactNode } from 'react'
import { Link, useRouter } from '@tanstack/react-router'
import {
  Database,
  Eye,
  FileText,
  Boxes,
  MapPin,
  Mail,
  Server,
} from 'lucide-react'

import { LocationPickerDialog, type ZoneLocation } from '@/components/zone/location-picker-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Fetch } from '@/lib/fetch'
import { cn } from '@/lib/utils'
import { PageContent } from '@/components/layout/layout'

type ZoneStatus = 'planned' | 'active' | 'degraded' | 'maintenance' | 'disabled'

type ServiceKey = 'hypervisor' | 'storage' | 'smtp' | 'kubernetes'

const serviceItems: Array<{ key: ServiceKey; label: string; icon: ElementType }> = [
  { key: 'hypervisor', label: 'Hypervisor', icon: Server },
  { key: 'storage', label: 'Storage', icon: Database },
  { key: 'smtp', label: 'SMTP', icon: Mail },
  { key: 'kubernetes', label: 'Kubernetes', icon: Boxes },
]

function FieldHint({ children }: { children: ReactNode }) {
  return <p className="mt-2 text-sm text-muted-foreground">{children}</p>
}

function Required() {
  return <span className="text-destructive">*</span>
}


const statusLabels: Record<ZoneStatus, string> = {
  planned: 'Planned',
  active: 'Active',
  degraded: 'Degraded',
  maintenance: 'Maintenance',
  disabled: 'Disabled',
}

function slugifyZoneCode(value: string) {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

async function readAPIMessage(response: Response) {
  try {
    const payload = (await response.json()) as { message?: string; error?: string }
    return payload.message || payload.error || 'Request failed'
  } catch {
    return 'Request failed'
  }
}

function PreviewStatus({ status }: { status: ZoneStatus }) {
  const label = statusLabels[status]
  return (
    <Badge
      variant="outline"
      className={cn(
        'h-8 rounded-lg px-3 text-sm font-medium',
        status === 'planned' && 'border-amber-200 bg-amber-50 text-amber-700',
        status === 'active' && 'border-emerald-200 bg-emerald-50 text-emerald-700',
        status === 'degraded' && 'border-orange-200 bg-orange-50 text-orange-700',
        status === 'maintenance' && 'border-violet-200 bg-violet-50 text-violet-700',
        status === 'disabled' && 'border-slate-200 bg-slate-50 text-slate-600',
      )}
    >
      {label}
    </Badge>
  )
}

export default function NewZonePage() {
  const router = useRouter()
  const [zoneName, setZoneName] = useState('')
  const [zoneCode, setZoneCode] = useState('')
  const [location, setLocation] = useState('')
  const [status, setStatus] = useState<ZoneStatus>('planned')
  const [description, setDescription] = useState('')
  const [locationDialogOpen, setLocationDialogOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [services, setServices] = useState<Record<ServiceKey, boolean>>({
    hypervisor: true,
    storage: true,
    smtp: false,
    kubernetes: true,
  })

  const toggleService = (key: ServiceKey) => {
    setServices((current) => ({ ...current, [key]: !current[key] }))
  }

  const selectLocation = (item: ZoneLocation) => {
    setLocation(item.label)
    if (!zoneCode.trim()) {
      setZoneCode(item.suggestedCode)
    }
  }

  const trimmedName = zoneName.trim()
  const trimmedCode = zoneCode.trim()
  const trimmedLocation = location.trim()
  const canSubmit = trimmedName !== '' && trimmedCode !== '' && trimmedLocation !== '' && !submitting

  const enabledServices = useMemo(
    () => Object.entries(services).filter(([, enabled]) => enabled).map(([key]) => key),
    [services],
  )

  const createZone = async () => {
    setError('')
    if (!canSubmit) {
      setError('Please fill in zone name, code, location, and status before creating the zone.')
      return
    }

    setSubmitting(true)
    try {
      const response = await Fetch('/admin/zones', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: trimmedName,
          code: slugifyZoneCode(trimmedCode),
          location: trimmedLocation,
          status,
          description: description.trim(),
          metadata: {
            enabled_services: enabledServices,
          },
        }),
      })

      if (!response.ok) {
        throw new Error(await readAPIMessage(response))
      }

      router.navigate({ to: '/zones' })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot create zone')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <PageContent className="pb-0">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-4">
          <nav className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
            <Link to="/zones" className="text-primary hover:underline">
              Zone
            </Link>
            <span>/</span>
            <span>Add Zone</span>
          </nav>
          <div className="space-y-2">
            <h1 className="text-3xl font-semibold tracking-[-0.03em] text-foreground md:text-4xl">
              Add Zone
            </h1>
            <p className="text-sm text-muted-foreground md:text-base">
              Create a new infrastructure zone for the platform.
            </p>
          </div>
        </div>

        <div className="flex items-center gap-3 lg:pt-10">
          <Button asChild variant="outline" className="h-12 rounded-lg px-8 text-sm font-semibold">
            <Link to="/zones">Cancel</Link>
          </Button>
          <Button className="h-12 rounded-lg px-8 text-sm font-semibold shadow-sm" onClick={createZone} disabled={!canSubmit}>
            {submitting ? 'Creating...' : 'Create Zone'}
          </Button>
        </div>
      </div>

      {error && (
        <div className="rounded-xl border border-destructive/20 bg-destructive/10 px-4 py-3 text-sm font-medium text-destructive">
          {error}
        </div>
      )}

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_400px]">
        <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7">
          <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Zone Details</h2>

          <div className="mt-6 grid gap-6 lg:grid-cols-2">
            <div>
              <Label className="text-sm font-semibold text-foreground">
                Zone Name <Required />
              </Label>
              <Input
                value={zoneName}
                onChange={(event) => setZoneName(event.target.value)}
                placeholder="e.g., US East 1"
                className="mt-3 h-12 rounded-lg border-border bg-background px-4 shadow-none"
              />
              <FieldHint>A friendly name to identify this zone.</FieldHint>
            </div>

            <div>
              <Label className="text-sm font-semibold text-foreground">
                Zone Code <Required />
              </Label>
              <Input
                value={zoneCode}
                onChange={(event) => setZoneCode(slugifyZoneCode(event.target.value))}
                placeholder="e.g., us-east-1"
                className="mt-3 h-12 rounded-lg border-border bg-background px-4 shadow-none"
              />
              <FieldHint>A unique code used for API and automation.</FieldHint>
            </div>

            <div>
              <Label className="text-sm font-semibold text-foreground">
                Location <Required />
              </Label>
              <Button
                type="button"
                variant="outline"
                onClick={() => setLocationDialogOpen(true)}
                className="mt-3 h-12 w-full justify-between rounded-lg border-border bg-background px-4 text-left font-medium shadow-none"
              >
                <span className="flex min-w-0 items-center gap-3">
                  <MapPin className="size-4 shrink-0 text-muted-foreground" />
                  <span className={cn('truncate', location ? 'text-foreground' : 'text-muted-foreground')}>
                    {location || 'Select location on world map'}
                  </span>
                </span>
                <span className="text-xs font-semibold text-primary">Open map</span>
              </Button>
              <FieldHint>Search the world map and choose the geographic location for this zone.</FieldHint>
            </div>

            <div>
              <Label className="text-sm font-semibold text-foreground">
                Status <Required />
              </Label>
              <Select value={status} onValueChange={(value) => setStatus(value as ZoneStatus)}>
                <SelectTrigger className="mt-3 h-12 w-full rounded-lg border-border bg-background px-4 shadow-none">
                  <SelectValue placeholder="Select status" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="planned">Planned</SelectItem>
                  <SelectItem value="active">Active</SelectItem>
                  <SelectItem value="degraded">Degraded</SelectItem>
                  <SelectItem value="maintenance">Maintenance</SelectItem>
                  <SelectItem value="disabled">Disabled</SelectItem>
                </SelectContent>
              </Select>
              <FieldHint>Initial operational status for the zone.</FieldHint>
            </div>
          </div>

          <div className="mt-7">
            <Label className="text-sm font-semibold text-foreground">Description</Label>
            <Textarea
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              placeholder="Describe the purpose, capacity, and intended workloads..."
              className="mt-3 min-h-28 rounded-lg border-border bg-background px-4 py-3 text-sm shadow-none"
            />
            <FieldHint>Provide details about this zone and its intended use.</FieldHint>
          </div>

          <div className="mt-8">
            <h3 className="text-sm font-semibold text-foreground">Enabled Services</h3>
            <p className="mt-2 text-sm text-muted-foreground">Select the platform services to enable in this zone.</p>
            <div className="mt-4 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
              {serviceItems.map((item) => {
                const Icon = item.icon
                return (
                  <div
                    key={item.key}
                    className="flex h-16 items-center justify-between rounded-lg border border-border bg-background px-4 text-left shadow-xs transition-colors hover:bg-muted/40"
                  >
                    <span className="flex items-center gap-3 text-sm font-medium text-foreground">
                      <Icon className="size-5 text-primary" />
                      {item.label}
                    </span>
                    <Switch checked={services[item.key]} onCheckedChange={() => toggleService(item.key)} />
                  </div>
                )
              })}
            </div>
          </div>
        </div>

        <aside className="space-y-6">
          <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7">
            <div className="mb-6 flex items-center justify-between">
              <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Zone Preview</h2>
              <Eye className="size-5 text-primary" />
            </div>

            <div className="space-y-5 text-sm">
              <div className="flex items-center justify-between gap-6">
                <span className="font-medium text-primary">Zone Name</span>
                <span className="text-right font-medium text-muted-foreground">{zoneName || '—'}</span>
              </div>
              <div className="flex items-center justify-between gap-6">
                <span className="font-medium text-primary">Location</span>
                <span className="text-right font-medium text-muted-foreground">{location || '—'}</span>
              </div>
              <div className="flex items-center justify-between gap-6">
                <span className="font-medium text-primary">Status</span>
                <PreviewStatus status={status} />
              </div>
            </div>

            <div className="my-7 border-t border-border" />

            <div>
              <p className="text-sm font-medium text-primary">Description</p>
              <p className="mt-4 text-sm italic leading-6 text-muted-foreground">
                {description.trim() || 'No description provided.'}
              </p>
            </div>
          </div>

          <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7">
            <div className="mb-6 flex items-center gap-3">
              <FileText className="size-5 text-primary" />
              <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Creation Notes</h2>
            </div>

            <div className="space-y-6">
              <NoteItem
                title="Choose a clear name and code"
                text="Use a descriptive name and a unique code so the zone is easy to identify."
              />
              <NoteItem
                title="Select the correct location and status"
                text="Pick the right location and initial status to match your deployment plan."
              />
              <NoteItem
                title="Enable only needed services"
                text="Turn on only the platform services required for this zone to keep it simple and secure."
              />
            </div>
          </div>
        </aside>
      </div>
      <LocationPickerDialog
        open={locationDialogOpen}
        value={location}
        onOpenChange={setLocationDialogOpen}
        onSelect={selectLocation}
      />
    </PageContent>
  )
}

function NoteItem({ title, text }: { title: string; text: string }) {
  return (
    <div className="grid grid-cols-[12px_1fr] gap-4">
      <span className="mt-1.5 size-2 rounded-full bg-primary" />
      <div>
        <p className="text-sm font-semibold text-foreground">{title}</p>
        <p className="mt-1 text-sm leading-6 text-muted-foreground">{text}</p>
      </div>
    </div>
  )
}
