import { AlertTriangle, CheckCircle2, Clock3 } from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import type { HypervisorAlert } from '@/lib/hypervisor'
import { cn } from '@/lib/utils'

type NodeAlertsMaintenanceProps = {
  alerts: HypervisorAlert[]
  loading: boolean
}

export function NodeAlertsMaintenance({ alerts, loading }: NodeAlertsMaintenanceProps) {
  return (
    <div className="rounded-xl border border-border bg-card p-6 shadow-xs">
      <div className="mb-5">
        <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Node Alerts & Maintenance</h2>
        <p className="mt-1 text-sm text-muted-foreground">Current health exceptions and maintenance-facing state.</p>
      </div>

      {loading ? (
        <div className="space-y-3">
          {Array.from({ length: 4 }).map((_, index) => (
            <div key={index} className="flex items-start gap-3 rounded-lg border border-border/70 p-4">
              <Skeleton className="size-10 rounded-lg" />
              <div className="min-w-0 flex-1 space-y-2">
                <div className="flex items-center justify-between gap-2">
                  <Skeleton className="h-4 w-32" />
                  <Skeleton className="h-6 w-16 rounded-md" />
                </div>
                <Skeleton className="h-4 w-full" />
                <Skeleton className="h-4 w-3/4" />
                <Skeleton className="h-3 w-20" />
              </div>
            </div>
          ))}
        </div>
      ) : alerts.length === 0 ? (
        <div className="flex h-[320px] items-center justify-center text-sm text-muted-foreground">
          No active alerts.
        </div>
      ) : (
        <div className="space-y-3">
          {alerts.map((alert) => (
            <div key={alert.id} className="flex items-start gap-3 rounded-lg border border-border/70 p-4">
              <div className={cn('mt-0.5 rounded-lg p-2', iconTone(alert.severity))}>
                <AlertIcon severity={alert.severity} />
              </div>
              <div className="min-w-0 flex-1 space-y-2">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <p className="text-sm font-semibold text-foreground">{alert.hostname}</p>
                  <Badge variant="outline" className={cn('h-6 rounded-md px-2 text-[10px] font-semibold uppercase', badgeTone(alert.severity))}>
                    {alert.severity}
                  </Badge>
                </div>
                <p className="text-sm text-muted-foreground">{alert.message}</p>
                <div className="flex items-center gap-2 text-xs text-muted-foreground">
                  <Clock3 className="size-3.5" />
                  {formatRelativeTime(alert.created_at)}
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function AlertIcon({ severity }: { severity: string }) {
  if (severity === 'critical' || severity === 'warning') {
    return <AlertTriangle className="size-4" />
  }
  return <CheckCircle2 className="size-4" />
}

function iconTone(severity: string) {
  switch (severity) {
    case 'critical':
      return 'bg-destructive/10 text-destructive'
    case 'warning':
      return 'bg-amber-50 text-amber-700'
    default:
      return 'bg-emerald-50 text-emerald-700'
  }
}

function badgeTone(severity: string) {
  switch (severity) {
    case 'critical':
      return 'border-destructive/20 bg-destructive/10 text-destructive'
    case 'warning':
      return 'border-amber-200 bg-amber-50 text-amber-700'
    default:
      return 'border-emerald-200 bg-emerald-50 text-emerald-700'
  }
}

function formatRelativeTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return 'Unknown time'
  }
  const seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000))
  if (seconds < 60) {
    return `${seconds}s ago`
  }
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) {
    return `${minutes}m ago`
  }
  const hours = Math.floor(minutes / 60)
  if (hours < 24) {
    return `${hours}h ago`
  }
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}
