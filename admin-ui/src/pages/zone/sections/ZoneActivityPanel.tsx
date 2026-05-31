import type { ReactNode } from 'react'
import { Activity, CalendarClock } from 'lucide-react'

import { Panel } from './ZoneOverviewPanel'

export type ZoneActivity = {
  id: string
  action: string
  target_type: string
  target_id: string
  message: string
  actor_name: string
  created_at?: string
}

interface ZoneActivityPanelProps {
  activities: ZoneActivity[]
}

function EmptyPanelText({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed border-border bg-muted/20 p-6 text-center text-sm font-medium text-muted-foreground">
      {children}
    </div>
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

export default function ZoneActivityPanel({
  activities,
}: ZoneActivityPanelProps) {
  return (
    <Panel title="Recent Activity" icon={Activity}>
      {activities.length === 0 ? (
        <EmptyPanelText>No recent activity for this zone yet.</EmptyPanelText>
      ) : (
        <div className="divide-y divide-border">
          {activities.map((activity) => (
            <div key={activity.id} className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
              <CalendarClock className="mt-0.5 size-4 shrink-0 text-primary" />
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium text-foreground">{activity.message}</p>
                <p className="mt-1 text-xs text-muted-foreground">
                  {titleCase(activity.action)} · {activity.actor_name || 'System'}
                </p>
              </div>
              <span className="shrink-0 text-xs text-muted-foreground">{formatRelative(activity.created_at)}</span>
            </div>
          ))}
        </div>
      )}
    </Panel>
  )
}
