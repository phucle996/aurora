import type { ReactNode } from 'react'
import { PackageCheck, Server, Database, Layers3, CloudCog, Clock3 } from 'lucide-react'

import { Panel } from './ZoneOverviewPanel'
import { cn } from '@/lib/utils'

export type ZoneServiceHealth = {
  key: string
  label: string
  desired_state: 'enable' | 'disable'
  actual_state: string
  status?: string
  source?: string
}

interface ZoneServicesPanelProps {
  enabledServices: ZoneServiceHealth[]
}

function titleCase(value: string) {
  return value
    .replace(/[_-]+/g, ' ')
    .split(' ')
    .filter(Boolean)
    .map((item) => item.charAt(0).toUpperCase() + item.slice(1))
    .join(' ')
}

function EmptyPanelText({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed border-border bg-muted/20 p-6 text-center text-sm font-medium text-muted-foreground">
      {children}
    </div>
  )
}

function ServiceIcon({ serviceKey }: { serviceKey: string }) {
  const Icon = serviceKey === 'hypervisor'
    ? Server
    : serviceKey === 'storage'
      ? Database
      : serviceKey === 'kubernetes'
        ? Layers3
        : serviceKey === 'mail'
          ? PackageCheck
          : serviceKey === 'managed_service'
            ? CloudCog
            : Clock3
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
    case 'unhealthy':
      return 'Unhealthy'
    case 'down':
      return 'Down'
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
    case 'unhealthy':
      return 'bg-rose-500'
    case 'down':
      return 'bg-red-500'
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
    case 'unhealthy':
      return 'text-rose-600'
    case 'down':
      return 'text-red-600'
    case 'disabled':
      return 'text-slate-500'
    default:
      return 'text-muted-foreground'
  }
}

export default function ZoneServicesPanel({
  enabledServices,
}: ZoneServicesPanelProps) {
  // [COMMENT]: Chỉ hiển thị những service được kích hoạt (desired_state = enable) ở panel tóm tắt này
  const activeServices = enabledServices.filter((s) => s.desired_state === 'enable')

  return (
    <Panel title="Enabled Services" icon={PackageCheck}>
      <div className="grid gap-4 sm:grid-cols-2">
        {activeServices.length === 0 && <EmptyPanelText>No enabled services configured yet.</EmptyPanelText>}
        {activeServices.map((service) => (
          <div
            key={service.key}
            className="flex h-12 items-center justify-between rounded-lg border border-border bg-background px-4 shadow-xs transition-colors hover:border-primary/25 hover:bg-primary/3"
          >
            <div className="flex items-center gap-3">
              <ServiceIcon serviceKey={service.key} />
              <p className="text-sm font-medium text-primary">{service.label || titleCase(service.key)}</p>
            </div>
            <div className="flex shrink-0 items-center gap-2 text-sm font-medium">
              <span className={cn('size-2 rounded-full', serviceStatusDotTone(service.actual_state))} />
              <span className={serviceStatusTextTone(service.actual_state)}>{serviceStatusLabel(service.actual_state)}</span>
            </div>
          </div>
        ))}
      </div>
    </Panel>
  )
}
