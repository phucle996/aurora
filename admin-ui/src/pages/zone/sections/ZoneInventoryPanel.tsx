import type { ReactNode } from 'react'
import { Layers3, Server, PackageCheck, Database, Box } from 'lucide-react'

import { Panel } from './ZoneOverviewPanel'
import { cn } from '@/lib/utils'

export type ZoneInventoryMetric = {
  key: string
  label: string
  value: number
  status: string
  source: string
}

interface ZoneInventoryPanelProps {
  metrics: ZoneInventoryMetric[]
}

function EmptyPanelText({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed border-border bg-muted/20 p-6 text-center text-sm font-medium text-muted-foreground">
      {children}
    </div>
  )
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

function splitInventoryColumns<T>(items: T[]): T[][] {
  const half = Math.ceil(items.length / 2)
  const left = items.slice(0, half)
  const right = items.slice(half)
  return right.length > 0 ? [left, right] : [left]
}

export default function ZoneInventoryPanel({
  metrics,
}: ZoneInventoryPanelProps) {
  return (
    <Panel title="Resource Inventory" icon={Layers3}>
      {metrics.length === 0 ? (
        <EmptyPanelText>No inventory sources connected yet.</EmptyPanelText>
      ) : (
        <div className="grid gap-x-10 gap-y-3 sm:grid-cols-2">
          {splitInventoryColumns(metrics).map((column, columnIndex) => (
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
  )
}
