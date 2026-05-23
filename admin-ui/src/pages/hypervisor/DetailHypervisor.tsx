import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { Activity, AlertTriangle, ChevronLeft, Cpu, Database, Network, RefreshCcw, Server, Trash2 } from 'lucide-react'
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { PageContent } from '@/components/layout/layout'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Progress } from '@/components/ui/progress'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  deleteHypervisorAgent,
  fetchHypervisorAgentDetail,
  fetchHypervisorAgentMetrics,
  hypervisorAgentStreamURL,
  type HypervisorAgentDetail,
  type HypervisorAgentMetric,
} from '@/lib/hypervisor'

function getAgentIdFromPath() {
  const segments = window.location.pathname.split('/').filter(Boolean)
  return decodeURIComponent(segments[1] ?? '')
}

export default function DetailHypervisorPage() {
  const navigate = useNavigate()
  const agentId = useMemo(() => getAgentIdFromPath(), [])
  const [detail, setDetail] = useState<HypervisorAgentDetail | null>(null)
  const [metrics, setMetrics] = useState<HypervisorAgentMetric[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleteConfirm, setDeleteConfirm] = useState('')
  const [deleting, setDeleting] = useState(false)

  const loadData = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [nextDetail, nextMetrics] = await Promise.all([
        fetchHypervisorAgentDetail(agentId),
        fetchHypervisorAgentMetrics(agentId, 120),
      ])
      setDetail(nextDetail)
      setMetrics(nextMetrics.reverse())
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot load hypervisor agent')
    } finally {
      setLoading(false)
    }
  }, [agentId])

  useEffect(() => {
    const timer = window.setTimeout(() => void loadData(), 0)
    return () => window.clearTimeout(timer)
  }, [loadData])

  useEffect(() => {
    if (!agentId) return
    const socket = new WebSocket(hypervisorAgentStreamURL(agentId))
    socket.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data) as { type?: string; data?: HypervisorAgentMetric }
        if (payload.type === 'node_metric' && payload.data) {
          const metric = payload.data
          setMetrics((current) => [...current.slice(-119), metric])
        }
        if (payload.type === 'agent_heartbeat' || payload.type === 'inventory_refreshed') {
          void loadData()
        }
      } catch {
        // Ignore malformed stream payloads; the next refresh will reconcile state.
      }
    }
    return () => socket.close()
  }, [agentId, loadData])

  const agent = detail?.agent
  const totalCores = useMemo(() => sum(detail?.cpu_packages ?? [], (item) => item.cores), [detail?.cpu_packages])
  const totalThreads = useMemo(() => sum(detail?.cpu_packages ?? [], (item) => item.threads), [detail?.cpu_packages])
  const totalMemoryGib = useMemo(() => sum(detail?.memory_modules ?? [], (item) => item.size_gib), [detail?.memory_modules])
  const totalStorageGib = useMemo(() => sum(detail?.storage_pools ?? [], (item) => item.total_gib), [detail?.storage_pools])
  const totalGPUMemoryGib = useMemo(() => sum(detail?.gpu_devices ?? [], (item) => item.memory_gib), [detail?.gpu_devices])
  const gpuCount = detail?.gpu_devices.length ?? 0
  const deleteTargetHostname = (agent?.hostname || '').trim()
  const canDelete = deleteTargetHostname !== '' && deleteConfirm.trim() === deleteTargetHostname

  async function handleDeleteAgent() {
    if (!agent?.id || !canDelete) return
    setDeleting(true)
    setError('')
    try {
      await deleteHypervisorAgent(agent.id)
      setDeleteOpen(false)
      setDeleteConfirm('')
      await navigate({ to: '/hypervisor' })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot delete hypervisor agent')
    } finally {
      setDeleting(false)
    }
  }

  if (loading && !detail) {
    return <DetailHypervisorSkeleton />
  }

  return (
    <PageContent>
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-3">
          <Link to="/hypervisor" className="inline-flex items-center gap-2 text-sm font-semibold text-muted-foreground hover:text-foreground">
            <ChevronLeft className="size-4" />
            Hypervisor
          </Link>
          <div>
            <h1 className="text-3xl font-semibold tracking-[-0.03em] text-foreground">{agent?.hostname || 'Hypervisor Agent'}</h1>
            <p className="mt-2 text-sm text-muted-foreground">
              {agent?.management_ip || 'No management IP'} · {agent?.zone_id || 'Unassigned zone'}
            </p>
          </div>
        </div>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
          <Button
            type="button"
            variant="destructive"
            className="h-11 rounded-lg px-4 text-sm font-semibold"
            onClick={() => {
              setDeleteConfirm('')
              setDeleteOpen(true)
            }}
            disabled={loading || !agent?.id}
          >
            <Trash2 className="mr-2 size-4" />
            Delete Agent
          </Button>
          <Button type="button" variant="outline" className="h-11 rounded-lg px-4 text-sm font-semibold" onClick={() => void loadData()} disabled={loading}>
            <RefreshCcw className="mr-2 size-4" />
            Refresh
          </Button>
        </div>
      </div>

      {error ? (
        <div className="mt-6 rounded-xl border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">
          {error}
        </div>
      ) : null}

      <div className="mt-6 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        <MetricCard icon={Server} label="Agent" value={agent?.status || '-'} detail={`Version ${agent?.version || '-'} · ${agent?.agent_id || '-'}`} />
        <MetricCard icon={Cpu} label="CPU Inventory" value={`${totalCores} cores`} detail={`${totalThreads} threads across ${detail?.cpu_packages.length ?? 0} packages`} />
        <MetricCard icon={Activity} label="Memory Inventory" value={`${totalMemoryGib} GiB`} detail={`${detail?.memory_modules.length ?? 0} module(s)`} />
        <MetricCard icon={Database} label="Storage Inventory" value={`${totalStorageGib} GiB`} detail={`${detail?.storage_pools.length ?? 0} pool(s) · ${gpuCount} GPU(s)`} />
      </div>

      <div className="mt-6 grid gap-6 xl:grid-cols-[minmax(0,1.75fr)_minmax(320px,1fr)]">
        <section className="rounded-xl border border-border bg-card p-6 shadow-xs">
          <div className="flex items-center justify-between gap-4">
            <div>
              <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Runtime Metrics</h2>
              <p className="mt-1 text-sm text-muted-foreground">Prometheus-backed samples streamed over controlplane websocket.</p>
            </div>
            <span className="text-xs text-muted-foreground">{detail?.latest_metric?.sampled_at ? `Latest ${formatDateTime(detail.latest_metric.sampled_at)}` : 'No latest sample'}</span>
          </div>
          <div className="mt-5 h-[320px]">
            <ResponsiveContainer width="100%" height="100%">
              <LineChart
                data={metrics.map((item) => ({
                  time: formatTime(item.sampled_at || ''),
                  rx: (item?.network_rx_bps ?? 0) / 1024 / 1024,
                  tx: (item?.network_tx_bps ?? 0) / 1024 / 1024,
                  read: (item?.disk_read_bps ?? 0) / 1024 / 1024,
                  write: (item?.disk_write_bps ?? 0) / 1024 / 1024,
                }))}
              >
                <XAxis dataKey="time" tickLine={false} axisLine={false} fontSize={10} interval="preserveStartEnd" />
                <YAxis tickLine={false} axisLine={false} fontSize={10} width={45} tickFormatter={(v: number) => `${v.toFixed(1)} MB/s`} />
                <Tooltip formatter={(value, name) => [`${Number(value).toFixed(2)} MB/s`, name]} contentStyle={{ borderRadius: 8, fontSize: 11 }} />
                <Line type="monotone" dataKey="rx" stroke="#3b82f6" strokeWidth={2} dot={false} name="Net RX" isAnimationActive={false} />
                <Line type="monotone" dataKey="tx" stroke="#f59e0b" strokeWidth={2} dot={false} name="Net TX" isAnimationActive={false} />
                <Line type="monotone" dataKey="read" stroke="#ef4444" strokeWidth={2} dot={false} name="Disk Read" isAnimationActive={false} />
                <Line type="monotone" dataKey="write" stroke="#14b8a6" strokeWidth={2} dot={false} name="Disk Write" isAnimationActive={false} />
              </LineChart>
            </ResponsiveContainer>
          </div>
        </section>

        <section className="rounded-xl border border-border bg-card p-6 shadow-xs">
          <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Capacity Utilization</h2>
          <div className="mt-5 space-y-6">
            <UsageRow
              label="CPU"
              value={detail?.latest_metric?.cpu_used_percent ?? 0}
              suffix="%"
              detail={`${(detail?.latest_metric?.cpu_used_cores ?? 0).toFixed(1)} / ${totalCores} cores (${totalThreads} threads)`}
            />
            <UsageRow
              label="Memory"
              value={detail?.latest_metric?.ram_used_percent ?? 0}
              suffix="%"
              detail={`${(detail?.latest_metric?.ram_used_gib ?? 0).toFixed(1)} / ${totalMemoryGib} GiB`}
            />
            <UsageRow
              label="Storage"
              value={detail?.latest_metric?.ssd_used_percent ?? 0}
              suffix="%"
              detail={`${(detail?.latest_metric?.ssd_used_gib ?? 0).toFixed(1)} / ${totalStorageGib} GiB`}
            />
            {gpuCount > 0 ? (
              <UsageRow
                label="GPU"
                value={detail?.latest_metric?.gpu_used_percent ?? 0}
                suffix="%"
                detail={`${(detail?.latest_metric?.gpu_used_gib ?? 0).toFixed(1)} / ${totalGPUMemoryGib.toFixed(1)} GiB`}
              />
            ) : null}
          </div>
        </section>
      </div>

      <div className="mt-6 grid gap-6 xl:grid-cols-2">
        <InventorySection title="CPU Packages" icon={Cpu}>
          <Table>
            <TableHeader className="bg-muted/30">
              <TableRow>
                <TableHead>Package</TableHead>
                <TableHead>Model</TableHead>
                <TableHead>Cores</TableHead>
                <TableHead>Threads</TableHead>
                <TableHead>Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(detail?.cpu_packages ?? []).map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="font-medium">#{item.package_index}</TableCell>
                  <TableCell>{item.model || '-'}</TableCell>
                  <TableCell>{item.cores}</TableCell>
                  <TableCell>{item.threads}</TableCell>
                  <TableCell>{formatDateTime(item.updated_at)}</TableCell>
                </TableRow>
              ))}
              {(detail?.cpu_packages.length ?? 0) === 0 ? <EmptyRow colSpan={5} /> : null}
            </TableBody>
          </Table>
        </InventorySection>

        <InventorySection title="Memory Modules" icon={Server}>
          <Table>
            <TableHeader className="bg-muted/30">
              <TableRow>
                <TableHead>Slot</TableHead>
                <TableHead>Model</TableHead>
                <TableHead>Capacity</TableHead>
                <TableHead>Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(detail?.memory_modules ?? []).map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="font-medium">#{item.slot_index}</TableCell>
                  <TableCell>{item.model || '-'}</TableCell>
                  <TableCell>{item.size_gib} GiB</TableCell>
                  <TableCell>{formatDateTime(item.updated_at)}</TableCell>
                </TableRow>
              ))}
              {(detail?.memory_modules.length ?? 0) === 0 ? <EmptyRow colSpan={4} /> : null}
            </TableBody>
          </Table>
        </InventorySection>

        <InventorySection title="GPU Devices" icon={Activity}>
          <Table>
            <TableHeader className="bg-muted/30">
              <TableRow>
                <TableHead>Device</TableHead>
                <TableHead>Model</TableHead>
                <TableHead>Memory</TableHead>
                <TableHead>Core Count</TableHead>
                <TableHead>Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(detail?.gpu_devices ?? []).map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="font-medium">#{item.device_index}</TableCell>
                  <TableCell>{item.model || '-'}</TableCell>
                  <TableCell>{item.memory_gib} GiB</TableCell>
                  <TableCell>{item.core_count}</TableCell>
                  <TableCell>{formatDateTime(item.updated_at)}</TableCell>
                </TableRow>
              ))}
              {(detail?.gpu_devices.length ?? 0) === 0 ? <EmptyRow colSpan={5} /> : null}
            </TableBody>
          </Table>
        </InventorySection>

        <InventorySection title="Storage Pools" icon={Database}>
          <Table>
            <TableHeader className="bg-muted/30">
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Driver</TableHead>
                <TableHead>Path</TableHead>
                <TableHead>Capacity</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(detail?.storage_pools ?? []).map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="font-medium">{item.name || '-'}</TableCell>
                  <TableCell>{item.driver || '-'}</TableCell>
                  <TableCell className="font-mono text-xs">{item.path || '-'}</TableCell>
                  <TableCell>{item.total_gib} GiB</TableCell>
                  <TableCell>{item.status || '-'}</TableCell>
                </TableRow>
              ))}
              {(detail?.storage_pools.length ?? 0) === 0 ? <EmptyRow colSpan={5} /> : null}
            </TableBody>
          </Table>
        </InventorySection>

        <InventorySection title="Network Interfaces" icon={Network}>
          <Table>
            <TableHeader className="bg-muted/30">
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>IPv4</TableHead>
                <TableHead>IPv6</TableHead>
                <TableHead>Speed</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(detail?.network_interfaces ?? []).map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="font-medium">{item.name || item.mac_address || '-'}</TableCell>
                  <TableCell>{item.ipv4_address || '-'}</TableCell>
                  <TableCell>{item.ipv6_address || '-'}</TableCell>
                  <TableCell>{item.speed_mbps} Mbps</TableCell>
                  <TableCell>{item.status || '-'}</TableCell>
                </TableRow>
              ))}
              {(detail?.network_interfaces.length ?? 0) === 0 ? <EmptyRow colSpan={5} /> : null}
            </TableBody>
          </Table>
        </InventorySection>
      </div>

      <section className="mt-6 rounded-xl border border-border bg-card p-6 shadow-xs">
        <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">VPS On Agent</h2>
        <div className="mt-5 overflow-x-auto rounded-lg border border-border">
          <Table>
            <TableHeader className="bg-muted/30">
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Power</TableHead>
                <TableHead>CPU</TableHead>
                <TableHead>RAM</TableHead>
                <TableHead>Primary IP</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(detail?.vps_instances ?? []).map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="font-medium">{item.name || item.hostname}</TableCell>
                  <TableCell>{item.status}</TableCell>
                  <TableCell>{item.power_state}</TableCell>
                  <TableCell>{item.vcpu_count}</TableCell>
                  <TableCell>{item.ram_gib} GiB</TableCell>
                  <TableCell className="font-mono text-xs">{item.primary_ipv4 || item.primary_ipv6 || '-'}</TableCell>
                </TableRow>
              ))}
              {!loading && (detail?.vps_instances.length ?? 0) === 0 ? <EmptyRow colSpan={6} message="No VPS instances on this agent." /> : null}
            </TableBody>
          </Table>
        </div>
      </section>

      <section className="mt-6 rounded-xl border border-border bg-card p-6 shadow-xs">
        <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Recent Events</h2>
        <div className="mt-5 overflow-x-auto rounded-lg border border-border">
          <Table>
            <TableHeader className="bg-muted/30">
              <TableRow>
                <TableHead>Action</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Message</TableHead>
                <TableHead>Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(detail?.recent_events ?? []).map((item) => (
                <TableRow key={item.id}>
                  <TableCell className="font-medium">{item.action || '-'}</TableCell>
                  <TableCell>{item.target_type || '-'} · {item.target_id || '-'}</TableCell>
                  <TableCell>{item.message || '-'}</TableCell>
                  <TableCell>{formatDateTime(item.created_at)}</TableCell>
                </TableRow>
              ))}
              {(detail?.recent_events.length ?? 0) === 0 ? <EmptyRow colSpan={4} message="No recent events." /> : null}
            </TableBody>
          </Table>
        </div>
      </section>

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete hypervisor agent</DialogTitle>
            <DialogDescription>
              This permanently removes the agent record. Type <span className="font-semibold text-foreground">{deleteTargetHostname || 'the hostname'}</span> to confirm.
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 text-sm text-amber-700 dark:text-amber-300">
              <div className="flex items-start gap-2">
                <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                <p>Deleting the agent record does not delete VPS instances automatically. Make sure the host is drained first.</p>
              </div>
            </div>
            <Input value={deleteConfirm} onChange={(event) => setDeleteConfirm(event.target.value)} placeholder={deleteTargetHostname || 'hostname'} autoComplete="off" />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setDeleteOpen(false)} disabled={deleting}>Cancel</Button>
            <Button type="button" variant="destructive" onClick={() => void handleDeleteAgent()} disabled={!canDelete || deleting}>
              {deleting ? 'Deleting...' : 'Delete Agent'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </PageContent>
  )
}

function MetricCard({ icon: Icon, label, value, detail }: { icon: typeof Server; label: string; value: string; detail: string }) {
  return (
    <div className="rounded-xl border border-border bg-card p-5 shadow-xs">
      <div className="flex items-center justify-between">
        <Icon className="size-5 text-primary" />
        <span className="text-xs font-semibold uppercase text-muted-foreground">{label}</span>
      </div>
      <p className="mt-4 text-2xl font-semibold text-foreground">{value}</p>
      <p className="mt-1 text-sm text-muted-foreground">{detail}</p>
    </div>
  )
}

function UsageRow({ label, value, suffix, detail }: { label: string; value: number; suffix: string; detail?: string }) {
  const normalized = Math.max(0, Math.min(100, Number.isFinite(value) ? value : 0))
  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between text-sm">
        <span className="font-medium text-foreground">{label}</span>
        <span className="font-semibold text-muted-foreground">{normalized.toFixed(1)}{suffix}</span>
      </div>
      <Progress value={normalized} className="h-2" />
      {detail ? <p className="text-right text-xs text-muted-foreground">{detail}</p> : null}
    </div>
  )
}

function InventorySection({ title, icon: Icon, children }: { title: string; icon: typeof Server; children: ReactNode }) {
  return (
    <section className="rounded-xl border border-border bg-card p-6 shadow-xs">
      <div className="flex items-center gap-2">
        <Icon className="size-5 text-primary" />
        <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">{title}</h2>
      </div>
      <div className="mt-5 overflow-x-auto rounded-lg border border-border">
        {children}
      </div>
    </section>
  )
}

function EmptyRow({ colSpan, message = 'No inventory yet.' }: { colSpan: number; message?: string }) {
  return (
    <TableRow>
      <TableCell colSpan={colSpan} className="h-20 text-center text-sm text-muted-foreground">
        {message}
      </TableCell>
    </TableRow>
  )
}

function DetailHypervisorSkeleton() {
  return (
    <PageContent>
      <div className="space-y-3">
        <Skeleton className="h-5 w-28" />
        <Skeleton className="h-10 w-72" />
        <Skeleton className="h-5 w-56" />
      </div>
      <div className="mt-6 grid gap-4 md:grid-cols-2 xl:grid-cols-4">
        {Array.from({ length: 4 }).map((_, index) => (
          <div key={index} className="rounded-xl border border-border bg-card p-5 shadow-xs">
            <Skeleton className="h-5 w-24" />
            <Skeleton className="mt-4 h-8 w-32" />
            <Skeleton className="mt-2 h-4 w-40" />
          </div>
        ))}
      </div>
      <div className="mt-6 grid gap-6 xl:grid-cols-[minmax(0,1.75fr)_minmax(320px,1fr)]">
        <section className="rounded-xl border border-border bg-card p-6 shadow-xs">
          <Skeleton className="h-6 w-40" />
          <Skeleton className="mt-5 h-[320px] w-full" />
        </section>
        <section className="rounded-xl border border-border bg-card p-6 shadow-xs">
          <Skeleton className="h-6 w-40" />
          <div className="mt-5 space-y-6">
            {Array.from({ length: 4 }).map((_, index) => (
              <div key={index} className="space-y-2">
                <div className="flex items-center justify-between">
                  <Skeleton className="h-4 w-24" />
                  <Skeleton className="h-4 w-16" />
                </div>
                <Skeleton className="h-2 w-full" />
                <Skeleton className="ml-auto h-3 w-28" />
              </div>
            ))}
          </div>
        </section>
      </div>
      <div className="mt-6 grid gap-6 xl:grid-cols-2">
        {Array.from({ length: 5 }).map((_, index) => (
          <section key={index} className="rounded-xl border border-border bg-card p-6 shadow-xs">
            <Skeleton className="h-6 w-40" />
            <div className="mt-5 space-y-3">
              {Array.from({ length: 4 }).map((__, row) => (
                <div key={row} className="grid grid-cols-4 gap-3 rounded-lg border border-border/60 p-3">
                  {Array.from({ length: 4 }).map((___, cell) => <Skeleton key={cell} className="h-4 w-full" />)}
                </div>
              ))}
            </div>
          </section>
        ))}
      </div>
    </PageContent>
  )
}

function sum<T>(items: T[], selector: (item: T) => number) {
  return items.reduce((total, item) => total + selector(item), 0)
}

function formatTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return new Intl.DateTimeFormat('en', { hour: '2-digit', minute: '2-digit' }).format(date)
}

function formatDateTime(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat('en', {
    year: 'numeric',
    month: 'short',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}
