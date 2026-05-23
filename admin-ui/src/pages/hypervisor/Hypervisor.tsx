import { useCallback, useEffect, useMemo, useState } from 'react'
import { Plus } from 'lucide-react'

import { NodeAlertsMaintenance } from '@/components/hypervisor/alerts-maintenance'
import { BootstrapKeyForm } from '@/components/hypervisor/bootstrap-key-form'
import { HypervisorStats } from '@/components/hypervisor/hypervisor-stats'
import { KVMNodeInventory } from '@/components/hypervisor/node-inventory'
import { ResourceUtilizationChart } from '@/components/hypervisor/utilization-chart'
import { Button } from '@/components/ui/button'
import { PageContent } from '@/components/layout/layout'
import { Sheet, SheetContent } from '@/components/ui/sheet'
import {
  fetchHypervisorAgents,
  fetchHypervisorOverview,
  fetchTopologyZones,
  type HypervisorAgentListResult,
  type HypervisorOverview,
  type ZoneOption,
} from '@/lib/hypervisor'

const pageSize = 10

export default function HypervisorPage() {
  const [nodesResult, setNodesResult] = useState<HypervisorAgentListResult>({ items: [], page: 1, limit: pageSize, total: 0 })
  const [overview, setOverview] = useState<HypervisorOverview | null>(null)
  const [zones, setZones] = useState<ZoneOption[]>([])
  const [query, setQuery] = useState('')
  const [zoneId, setZoneId] = useState('')
  const [status, setStatus] = useState('')
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [drawerOpen, setDrawerOpen] = useState(false)

  const loadData = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [nodes, overviewData, zoneItems] = await Promise.all([
        fetchHypervisorAgents({ page, limit: pageSize, search: query, zoneId, status }),
        fetchHypervisorOverview(),
        fetchTopologyZones(),
      ])
      setNodesResult(nodes)
      setOverview(overviewData)
      setZones(zoneItems)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot load hypervisor data')
      setNodesResult({ items: [], page, limit: pageSize, total: 0 })
      setOverview(null)
    } finally {
      setLoading(false)
    }
  }, [page, query, status, zoneId])

  useEffect(() => {
    const timer = window.setTimeout(() => void loadData(), 0)
    return () => window.clearTimeout(timer)
  }, [loadData])

  const zoneMap = useMemo(() => new Map(zones.map((zone) => [zone.id, zone])), [zones])
  const resolveZoneLabel = useCallback(
    (value: string) => {
      const zone = zoneMap.get(value)
      return zone ? zone.code : value
    },
    [zoneMap],
  )

  return (
    <>
      <PageContent>
        <div className="flex flex-col gap-5 lg:flex-row lg:items-start lg:justify-between">
          <div className="space-y-3">
            <nav className="flex items-center gap-2 text-sm font-semibold text-muted-foreground">
              <span className="text-primary">Infrastructure</span>
              <span>/</span>
              <span className="text-foreground">Hypervisor</span>
            </nav>
            <div>
              <h1 className="text-3xl font-semibold tracking-[-0.03em] text-foreground md:text-4xl">Hypervisor</h1>
              <p className="mt-2 max-w-2xl text-sm text-muted-foreground md:text-base">
                Create one-time bootstrap tokens, watch enrollment progress, and inspect enrolled KVM agent capacity by zone.
              </p>
            </div>
          </div>

          <div className="lg:pt-10">
            <Button className="h-11 rounded-lg px-6 text-sm font-semibold shadow-sm" onClick={() => setDrawerOpen(true)}>
              <Plus className="size-4" />
              Add New
            </Button>
          </div>
        </div>

        {error && (
          <div className="rounded-lg border border-destructive/20 bg-destructive/10 px-4 py-3 text-sm font-medium text-destructive">
            {error}
          </div>
        )}

        <HypervisorStats summary={overview?.summary ?? null} loading={loading} />

        <KVMNodeInventory
          nodes={nodesResult.items}
          total={nodesResult.total}
          page={page}
          limit={pageSize}
          query={query}
          zoneId={zoneId}
          status={status}
          zones={zones}
          loading={loading}
          error=""
          onQueryChange={(value) => {
            setQuery(value)
            setPage(1)
          }}
          onZoneChange={(value) => {
            setZoneId(value)
            setPage(1)
          }}
          onStatusChange={(value) => {
            setStatus(value)
            setPage(1)
          }}
          onPageChange={setPage}
          onRefresh={() => void loadData()}
          resolveZoneLabel={resolveZoneLabel}
        />

        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <ResourceUtilizationChart items={overview?.zone_utilization ?? []} loading={loading} resolveZoneLabel={resolveZoneLabel} />
          <NodeAlertsMaintenance alerts={overview?.alerts ?? []} loading={loading} />
        </div>
      </PageContent>

      <Sheet open={drawerOpen} onOpenChange={setDrawerOpen}>
        <SheetContent side="right" className="w-full overflow-y-auto border-l border-border/60 bg-background p-0 shadow-none sm:w-[60vw] sm:max-w-[60vw]">
          <div className="min-h-full px-6 py-6 sm:px-8">
            <BootstrapKeyForm open={drawerOpen} onOpenChange={setDrawerOpen} onEnrollmentReady={() => void loadData()} />
          </div>
        </SheetContent>
      </Sheet>
    </>
  )
}
