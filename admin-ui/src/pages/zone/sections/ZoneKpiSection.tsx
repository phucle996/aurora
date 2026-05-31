import type { ReactNode } from 'react'
import { MapPin, ShieldCheck, Server, Users, PackageCheck } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'

export type ZoneStatus = 'planned' | 'active' | 'degraded' | 'maintenance' | 'disabled'

const statusLabels: Record<ZoneStatus, string> = {
  planned: 'Planned',
  active: 'Active',
  degraded: 'Degraded',
  maintenance: 'Maintenance',
  disabled: 'Disabled',
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

function titleCase(value: string) {
  return value
    .replace(/[_-]+/g, ' ')
    .split(' ')
    .filter(Boolean)
    .map((item) => item.charAt(0).toUpperCase() + item.slice(1))
    .join(' ')
}

export function StatusBadge({ status }: { status: string }) {
  const label = status in statusLabels ? statusLabels[status as ZoneStatus] : titleCase(status || 'unknown')
  return (
    <Badge variant="outline" className={cn('h-7 rounded-lg border px-2.5 text-xs font-semibold', statusTone(status))}>
      {label}
    </Badge>
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

interface ZoneKpiSectionProps {
  location: string
  status: string
  hypervisorsValue?: string
  workspacesCount: number
  enabledServicesCount: number
}

export default function ZoneKpiSection({
  location,
  status,
  hypervisorsValue,
  workspacesCount,
  enabledServicesCount,
}: ZoneKpiSectionProps) {
  return (
    <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-5">
      <KpiCard icon={MapPin} label="Location" value={location} className="md:col-span-2" />
      <KpiCard icon={ShieldCheck} label="Status" value={<StatusBadge status={status} />} />
      {hypervisorsValue !== undefined && <KpiCard icon={Server} label="Hypervisors" value={hypervisorsValue} />}
      <KpiCard icon={Users} label="Workspaces" value={String(workspacesCount)} />
      <KpiCard icon={PackageCheck} label="Enabled Services" value={String(enabledServicesCount)} />
    </div>
  )
}
