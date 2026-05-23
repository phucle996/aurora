import { Link } from '@tanstack/react-router'
import { RefreshCcw, Search } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Progress } from '@/components/ui/progress'
import type { HypervisorAgentItem, ZoneOption } from '@/lib/hypervisor'
import { cn } from '@/lib/utils'

type KVMNodeInventoryProps = {
  nodes: HypervisorAgentItem[]
  total: number
  page: number
  limit: number
  query: string
  zoneId: string
  status: string
  zones: ZoneOption[]
  loading: boolean
  error: string
  onQueryChange: (value: string) => void
  onZoneChange: (value: string) => void
  onStatusChange: (value: string) => void
  onPageChange: (value: number) => void
  onRefresh: () => void
  resolveZoneLabel: (zoneId: string) => string
}

const statusOptions = [
  { value: 'all', label: 'All statuses' },
  { value: 'active', label: 'Active' },
  { value: 'provisioning', label: 'Provisioning' },
  { value: 'maintenance', label: 'Maintenance' },
  { value: 'degraded', label: 'Degraded' },
  { value: 'decommissioned', label: 'Decommissioned' },
]

export function KVMNodeInventory({
  nodes,
  total,
  page,
  limit,
  query,
  zoneId,
  status,
  zones,
  loading,
  error,
  onQueryChange,
  onZoneChange,
  onStatusChange,
  onPageChange,
  onRefresh,
  resolveZoneLabel,
}: KVMNodeInventoryProps) {
  const totalPages = Math.max(1, Math.ceil(total / limit))
  const startIndex = total === 0 ? 0 : (page - 1) * limit + 1
  const endIndex = total === 0 ? 0 : Math.min(page * limit, total)

  return (
    <div className="space-y-5 rounded-xl border border-border bg-card p-6 shadow-xs">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">KVM Node Inventory</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {loading ? 'Fetching node inventory and metrics...' : `${total} nodes in the current view.`}
          </p>
        </div>
        <div className="flex flex-col gap-3 md:flex-row md:items-center">
          <div className="relative min-w-0 md:w-72">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(event) => onQueryChange(event.target.value)}
              placeholder="Search hostname or IP..."
              className="h-11 rounded-lg border-border bg-background pl-10 shadow-none"
            />
          </div>
          <Select value={zoneId || 'all'} onValueChange={(value) => onZoneChange(value === 'all' ? '' : value)}>
            <SelectTrigger className="h-11 w-full rounded-lg md:w-48">
              <SelectValue placeholder="All zones" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">All zones</SelectItem>
              {zones.map((item) => (
                <SelectItem key={item.id} value={item.id}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Select value={status || 'all'} onValueChange={(value) => onStatusChange(value === 'all' ? '' : value)}>
            <SelectTrigger className="h-11 w-full rounded-lg md:w-44">
              <SelectValue placeholder="All statuses" />
            </SelectTrigger>
            <SelectContent>
              {statusOptions.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button type="button" variant="outline" className="h-11 rounded-lg px-4 text-sm font-semibold" onClick={onRefresh} disabled={loading}>
            <RefreshCcw className={cn('size-4', loading && 'animate-spin')} />
            Refresh
          </Button>
        </div>
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/20 bg-destructive/10 px-4 py-3 text-sm font-medium text-destructive">
          {error}
        </div>
      )}

      <div className="overflow-x-auto rounded-lg border border-border">
        <Table className="min-w-[1120px]">
          <TableHeader className="bg-muted/30">
            <TableRow>
              <TableHead className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Node</TableHead>
              <TableHead className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Zone</TableHead>
              <TableHead className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Management IP</TableHead>
              <TableHead className="w-[150px] text-xs font-semibold uppercase tracking-wide text-muted-foreground">vCPU Usage</TableHead>
              <TableHead className="w-[150px] text-xs font-semibold uppercase tracking-wide text-muted-foreground">Memory Usage</TableHead>
              <TableHead className="w-[150px] text-xs font-semibold uppercase tracking-wide text-muted-foreground">Storage Usage</TableHead>
              <TableHead className="text-center text-xs font-semibold uppercase tracking-wide text-muted-foreground">Running VPS</TableHead>
              <TableHead className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Status</TableHead>
              <TableHead className="w-[56px]" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading &&
              Array.from({ length: 6 }).map((_, index) => (
                <TableRow key={`loading-${index}`}>
                  <TableCell className="py-4">
                    <div className="space-y-2">
                      <Skeleton className="h-4 w-36" />
                      <Skeleton className="h-3 w-44" />
                    </div>
                  </TableCell>
                  <TableCell><Skeleton className="h-6 w-20 rounded-full" /></TableCell>
                  <TableCell><Skeleton className="h-6 w-24 rounded-full" /></TableCell>
                  <TableCell>
                    <div className="space-y-2">
                      <Skeleton className="h-3 w-16" />
                      <Skeleton className="h-2 w-full" />
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="space-y-2">
                      <Skeleton className="h-3 w-16" />
                      <Skeleton className="h-2 w-full" />
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="space-y-2">
                      <Skeleton className="h-3 w-16" />
                      <Skeleton className="h-2 w-full" />
                    </div>
                  </TableCell>
                  <TableCell>
                    <div className="space-y-2">
                      <Skeleton className="h-4 w-28" />
                      <Skeleton className="h-3 w-14" />
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            {!loading && nodes.length === 0 && (
              <TableRow>
                <TableCell colSpan={9} className="h-28 text-center text-sm font-medium text-muted-foreground">
                  No nodes match the current filters.
                </TableCell>
              </TableRow>
            )}

            {!loading &&
              nodes.map((node) => (
                <TableRow key={node.id} className="hover:bg-muted/30">
                  <TableCell>
                    <div className="space-y-1">
                      <Link
                        to="/hypervisor/$agentId"
                        params={{ agentId: node.id }}
                        className="inline-flex text-sm font-semibold text-foreground transition-colors hover:text-primary hover:underline"
                      >
                        {node.hostname}
                      </Link>
                      <p className="text-xs font-medium text-muted-foreground">{node.agent_version || 'Unknown version'}</p>
                    </div>
                  </TableCell>
                  <TableCell className="text-sm font-medium text-muted-foreground">{node.zone_id ? resolveZoneLabel(node.zone_id) : 'Unassigned'}</TableCell>
                  <TableCell className="font-mono text-sm font-medium text-muted-foreground">{node.management_ip || '—'}</TableCell>
                  <UsageCell value={node.vcpu_usage_percent} indicatorClassName="bg-primary" />
                  <UsageCell value={node.memory_usage_percent} indicatorClassName="bg-emerald-500" />
                  <UsageCell value={node.storage_usage_percent} indicatorClassName="bg-slate-500" />
                  <TableCell className="text-center">
                    <span className="text-sm font-semibold text-primary">{node.running_vps}</span>
                  </TableCell>
                  <TableCell>
                    <div className="space-y-1">
                      <div className="flex items-center gap-2">
                        <div className={cn('size-2.5 rounded-full', statusDotTone(node))} />
                        <span className={cn('text-sm font-semibold', statusTextTone(node))}>{statusLabel(node)}</span>
                      </div>
                      <p className="text-xs text-muted-foreground">{heartbeatLabel(node.last_heartbeat_at)}</p>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
          </TableBody>
        </Table>
      </div>

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-xs text-muted-foreground">
          {total === 0 ? 'Showing 0 nodes' : `Showing ${startIndex} to ${endIndex} of ${total} nodes`}
        </p>
        <div className="flex items-center gap-2">
          <Button type="button" variant="outline" size="sm" className="h-9 rounded-lg" disabled={page <= 1 || loading} onClick={() => onPageChange(page - 1)}>
            Previous
          </Button>
          <span className="text-sm text-muted-foreground">
            Page {page} / {totalPages}
          </span>
          <Button type="button" variant="outline" size="sm" className="h-9 rounded-lg" disabled={page >= totalPages || loading} onClick={() => onPageChange(page + 1)}>
            Next
          </Button>
        </div>
      </div>
    </div>
  )
}

function UsageCell({ value, indicatorClassName }: { value: number; indicatorClassName: string }) {
  const normalized = Number.isFinite(value) ? Math.max(0, Math.min(100, value)) : 0
  return (
    <TableCell>
      <div className="flex items-center gap-2">
        <span className="w-12 text-[11px] font-semibold text-foreground">{normalized.toFixed(1)}%</span>
        <Progress value={normalized} className="h-2" indicatorClassName={indicatorClassName} />
      </div>
    </TableCell>
  )
}

function statusLabel(node: HypervisorAgentItem) {
  if (node.agent_status === 'offline') {
    return 'Offline'
  }
  switch (node.status) {
    case 'active':
      return 'Active'
    case 'provisioning':
      return 'Provisioning'
    case 'maintenance':
      return 'Maintenance'
    case 'degraded':
      return 'Degraded'
    case 'decommissioned':
      return 'Decommissioned'
    default:
      return node.status
  }
}

function statusDotTone(node: HypervisorAgentItem) {
  if (node.agent_status === 'offline') {
    return 'bg-destructive'
  }
  switch (node.status) {
    case 'active':
      return 'bg-emerald-500'
    case 'maintenance':
      return 'bg-violet-500'
    case 'degraded':
      return 'bg-amber-500'
    default:
      return 'bg-slate-400'
  }
}

function statusTextTone(node: HypervisorAgentItem) {
  if (node.agent_status === 'offline') {
    return 'text-destructive'
  }
  switch (node.status) {
    case 'active':
      return 'text-emerald-700'
    case 'maintenance':
      return 'text-violet-700'
    case 'degraded':
      return 'text-amber-700'
    default:
      return 'text-muted-foreground'
  }
}

function heartbeatLabel(value?: string) {
  if (!value) {
    return 'No heartbeat yet'
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return 'Unknown heartbeat'
  }
  const seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000))
  if (seconds < 60) {
    return `${seconds}s ago`
  }
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) {
    return `${minutes}m ago`
  }
  return `${Math.floor(minutes / 60)}h ago`
}
