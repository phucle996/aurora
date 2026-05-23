import { Activity, Cpu, Database, Server, ShieldCheck } from 'lucide-react'

import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import type { HypervisorOverviewSummary } from '@/lib/hypervisor'

type HypervisorStatsProps = {
  summary: HypervisorOverviewSummary | null
  loading: boolean
}

export function HypervisorStats({ summary, loading }: HypervisorStatsProps) {
  const items = [
    {
      title: 'Total Nodes',
      value: summary?.total_nodes ?? 0,
      icon: Server,
    },
    {
      title: 'Healthy Nodes',
      value: summary?.healthy_nodes ?? 0,
      icon: ShieldCheck,
    },
    {
      title: 'Running VPS',
      value: summary?.running_vps ?? 0,
      icon: Activity,
    },
    {
      title: 'Total vCPU',
      value: summary?.total_vcpu_capacity ?? 0,
      icon: Cpu,
    },
    {
      title: 'Total RAM',
      value: `${summary?.total_ram_gib ?? 0} GiB`,
      icon: Database,
    },
  ]

  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-5">
      {items.map((item) => (
        <Card key={item.title} className="border-border bg-card py-0 shadow-xs">
          <CardContent className="flex min-h-28 items-center gap-4 p-5">
            <div className="flex size-12 shrink-0 items-center justify-center rounded-lg border border-primary/20 bg-primary/10 text-primary">
              <item.icon className="size-5" />
            </div>
            <div className="min-w-0 space-y-2">
              <p className="text-sm font-medium text-muted-foreground">{item.title}</p>
              {loading ? (
                <div className="space-y-2">
                  <Skeleton className="h-7 w-16" />
                  <Skeleton className="h-4 w-24" />
                </div>
              ) : (
                <p className="text-2xl font-semibold tracking-[-0.02em] text-foreground">{item.value}</p>
              )}
            </div>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
