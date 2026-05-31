import type { ElementType, ReactNode } from 'react'
import { Brain, Database, Boxes, Mail, Server } from 'lucide-react'

import { LocationAutocomplete, type ZoneLocation } from '@/components/zone/location-autocomplete'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

export type ServiceKey = 'hypervisor' | 'storage' | 'mail' | 'k8s' | 'ai'

const serviceItems: Array<{ key: ServiceKey; label: string; icon: ElementType }> = [
  { key: 'hypervisor', label: 'Hypervisor', icon: Server },
  { key: 'storage', label: 'Storage', icon: Database },
  { key: 'mail', label: 'Mail', icon: Mail },
  { key: 'k8s', label: 'Kubernetes', icon: Boxes },
  { key: 'ai', label: 'AI', icon: Brain },
]

function FieldHint({ children }: { children: ReactNode }) {
  return <p className="mt-2 text-sm text-muted-foreground">{children}</p>
}

function Required() {
  return <span className="text-destructive">*</span>
}

function liveSlugify(value: string) {
  return value
    .toLowerCase()
    .replace(/\s+/g, '-')
    .replace(/[^a-z0-9-]/g, '')
}

interface ZoneFormProps {
  zoneName: string
  setZoneName: (val: string) => void
  zoneCode: string
  setZoneCode: (val: string) => void
  isZoneCodeManuallyEdited: boolean
  setIsZoneCodeManuallyEdited: (val: boolean) => void
  location: string
  setLocation: (val: string) => void
  description: string
  setDescription: (val: string) => void
  services: Record<ServiceKey, boolean>
  toggleService: (key: ServiceKey) => void
  selectLocation: (item: ZoneLocation) => void
}

export default function ZoneForm({
  zoneName,
  setZoneName,
  zoneCode,
  setZoneCode,
  isZoneCodeManuallyEdited,
  setIsZoneCodeManuallyEdited,
  location,
  description,
  setDescription,
  services,
  toggleService,
  selectLocation,
}: ZoneFormProps) {
  return (
    <div className="rounded-xl border border-border bg-card p-6 shadow-xs md:p-7">
      <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Zone Details</h2>

      <div className="mt-6 grid gap-6 lg:grid-cols-2">
        <div>
          <Label className="text-sm font-semibold text-foreground">
            Zone Name <Required />
          </Label>
          <Input
            value={zoneName}
            onChange={(event) => {
              const val = event.target.value
              setZoneName(val)
              if (!isZoneCodeManuallyEdited) {
                setZoneCode(liveSlugify(val))
              }
            }}
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
            onChange={(event) => {
              const val = event.target.value
              setIsZoneCodeManuallyEdited(val !== '')
              setZoneCode(liveSlugify(val))
            }}
            placeholder="e.g., us-east-1"
            className="mt-3 h-12 rounded-lg border-border bg-background px-4 shadow-none"
          />
          <FieldHint>A unique code used for API and automation.</FieldHint>
        </div>

        <div className="lg:col-span-2">
          <Label className="text-sm font-semibold text-foreground">
            Location <Required />
          </Label>
          <LocationAutocomplete value={location} onSelect={selectLocation} />
          <FieldHint>Search and select the geographic location for this zone.</FieldHint>
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
        <div className="mt-4 grid gap-4 md:grid-cols-2 xl:grid-cols-5">
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
  )
}
