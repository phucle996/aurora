import { Bar, BarChart, CartesianGrid, Tooltip, XAxis, YAxis } from 'recharts'

import { ChartContainer, ChartTooltipContent, type ChartConfig } from '@/components/ui/chart'
import { Skeleton } from '@/components/ui/skeleton'
import type { HypervisorZoneUtilization } from '@/lib/hypervisor'

const chartConfig = {
  vcpu: {
    label: 'vCPU',
    color: '#2563eb',
  },
  memory: {
    label: 'Memory',
    color: '#10b981',
  },
  storage: {
    label: 'Storage',
    color: '#64748b',
  },
} satisfies ChartConfig

type ResourceUtilizationChartProps = {
  items: HypervisorZoneUtilization[]
  loading: boolean
  resolveZoneLabel: (zoneId: string) => string
}

export function ResourceUtilizationChart({ items, loading, resolveZoneLabel }: ResourceUtilizationChartProps) {
  const data = items.map((item) => ({
    zone: resolveZoneLabel(item.zone_id),
    vcpu: Number(item.vcpu_usage_percent.toFixed(1)),
    memory: Number(item.memory_usage_percent.toFixed(1)),
    storage: Number(item.storage_usage_percent.toFixed(1)),
  }))

  return (
    <div className="rounded-xl border border-border bg-card p-6 shadow-xs">
      <div className="mb-5 flex items-center justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Resource Utilization by Zone</h2>
          <p className="mt-1 text-sm text-muted-foreground">Latest node metrics aggregated by scheduling zone.</p>
        </div>
      </div>

      {loading ? (
        <div className="space-y-4">
          <div className="grid grid-cols-4 gap-3 px-2">
            {Array.from({ length: 4 }).map((_, index) => (
              <Skeleton key={index} className="h-4 w-full" />
            ))}
          </div>
          <div className="flex h-[272px] items-end gap-4 rounded-lg border border-border/50 px-4 py-5">
            {Array.from({ length: 6 }).map((_, index) => (
              <div key={index} className="flex flex-1 items-end gap-2">
                <Skeleton className="w-4 rounded-t-md" style={{ height: `${120 + (index % 3) * 28}px` }} />
                <Skeleton className="w-4 rounded-t-md" style={{ height: `${156 - (index % 2) * 22}px` }} />
                <Skeleton className="w-4 rounded-t-md" style={{ height: `${96 + (index % 4) * 20}px` }} />
              </div>
            ))}
          </div>
        </div>
      ) : data.length === 0 ? (
        <div className="flex h-[320px] items-center justify-center text-sm text-muted-foreground">
          No utilization data yet.
        </div>
      ) : (
        <div className="h-[320px] w-full">
          <ChartContainer config={chartConfig} className="h-full w-full">
            <BarChart data={data} margin={{ top: 16, right: 8, left: -12, bottom: 0 }}>
              <CartesianGrid vertical={false} strokeDasharray="3 3" stroke="#e2e8f0" />
              <XAxis
                dataKey="zone"
                axisLine={false}
                tickLine={false}
                tick={{ fontSize: 11, fontWeight: 600, fill: '#64748b' }}
                dy={8}
              />
              <YAxis
                axisLine={false}
                tickLine={false}
                tick={{ fontSize: 11, fontWeight: 600, fill: '#64748b' }}
                tickFormatter={(value) => `${value}%`}
              />
              <Tooltip cursor={{ fill: '#f8fafc' }} content={<ChartTooltipContent />} />
              <Bar dataKey="vcpu" fill="var(--color-vcpu)" radius={[4, 4, 0, 0]} barSize={16} />
              <Bar dataKey="memory" fill="var(--color-memory)" radius={[4, 4, 0, 0]} barSize={16} />
              <Bar dataKey="storage" fill="var(--color-storage)" radius={[4, 4, 0, 0]} barSize={16} />
            </BarChart>
          </ChartContainer>
        </div>
      )}
    </div>
  )
}
