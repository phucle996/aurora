/**
 * Hypervisor.tsx — Trang quản lý và giám sát hạ tầng các Hypervisor Nodes.
 *
 * Cho phép SRE giám sát dung lượng CPU, RAM, Storage của từng Hypervisor Node
 * thuộc về một Zone cụ thể. Hỗ trợ tìm kiếm nhanh, reload và phân trang.
 */

import { useCallback, useEffect, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  Search,
  RefreshCw,
  MoreVertical,
  Server,
  MapPin,
  ChevronLeft,
  ChevronRight,
  FileDown,
  Activity,
  Cpu,
  Database
} from 'lucide-react'

import { Button } from '@/components/ui/button'
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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  fetchTopologyZones,
  fetchHypervisorNodes,
  type ZoneOption,
  type HypervisorNodeItem
} from '@/lib/hypervisor'
import { cn } from '@/lib/utils'
import { PageContent } from '@/components/layout/layout'

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// Định dạng ngày tạo đẹp (Ví dụ: May 22, 2025)
function formatDate(dateStr?: string): string {
  if (!dateStr) return '—'
  try {
    const d = new Date(dateStr)
    if (isNaN(d.getTime())) return '—'
    return d.toLocaleDateString('en-US', {
      month: 'short',
      day: 'numeric',
      year: 'numeric'
    })
  } catch {
    return '—'
  }
}

// Tính relative time cho lần hoạt động cuối
function formatLastActive(dateStr?: string): string {
  if (!dateStr) return 'Offline'
  try {
    const d = new Date(dateStr)
    if (isNaN(d.getTime())) return 'Offline'
    const now = new Date()
    const diffMs = now.getTime() - d.getTime()
    const diffMins = Math.floor(diffMs / 60000)

    if (diffMins < 1) return 'Just now'
    if (diffMins < 60) return `${diffMins} minute${diffMins > 1 ? 's' : ''} ago`
    const diffHours = Math.floor(diffMins / 60)
    if (diffHours < 24) return `${diffHours} hour${diffHours > 1 ? 's' : ''} ago`
    const diffDays = Math.floor(diffHours / 24)
    return `${diffDays} day${diffDays > 1 ? 's' : ''} ago`
  } catch {
    return 'Offline'
  }
}

// Kiểm tra xem node có active gần đây hay không (dưới 5 phút)
function isNodeActive(dateStr?: string): boolean {
  if (!dateStr) return false
  try {
    const d = new Date(dateStr)
    if (isNaN(d.getTime())) return false
    const now = new Date()
    const diffMs = now.getTime() - d.getTime()
    return diffMs < 5 * 60 * 1000 // 5 phút
  } catch {
    return false
  }
}

// ----------------------------------------------------------------------------
// Main Component
// ----------------------------------------------------------------------------

export default function HypervisorPage() {
  const [zones, setZones] = useState<ZoneOption[]>([])
  const [selectedZoneId, setSelectedZoneId] = useState<string>('')
  const [nodes, setNodes] = useState<HypervisorNodeItem[]>([])
  const [searchQuery, setSearchQuery] = useState<string>('')
  
  const [loading, setLoading] = useState<boolean>(true)
  const [refreshing, setRefreshing] = useState<boolean>(false)
  const [error, setError] = useState<string>('')

  // State phân trang
  const [currentPage, setCurrentPage] = useState<number>(1)
  const [pageSize, setPageSize] = useState<number>(10)

  // 1. Fetch danh sách Zone ban đầu
  useEffect(() => {
    async function initZones() {
      try {
        setLoading(true)
        const zoneItems = await fetchTopologyZones()
        setZones(zoneItems)
        // [COMMENT]: Mặc định chọn Zone đầu tiên để tải dữ liệu tự động
        if (zoneItems.length > 0) {
          setSelectedZoneId(zoneItems[0].id)
        } else {
          setLoading(false)
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Cannot load zones')
        setLoading(false)
      }
    }
    void initZones()
  }, [])

  // 2. Fetch danh sách Nodes khi selectedZoneId thay đổi
  const loadNodes = useCallback(async (isRefresh = false) => {
    if (!selectedZoneId) return
    if (isRefresh) setRefreshing(true)
    else setLoading(true)
    
    setError('')
    try {
      const nodeList = await fetchHypervisorNodes(selectedZoneId)
      setNodes(nodeList)
      setCurrentPage(1)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot load hypervisor nodes')
      setNodes([])
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [selectedZoneId])

  useEffect(() => {
    void loadNodes()
  }, [loadNodes])

  // Trình xử lý thủ công reload
  const handleRefresh = () => {
    void loadNodes(true)
  }

  // 3. Thực hiện lọc danh sách Nodes client-side theo searchQuery
  const filteredNodes = nodes.filter((node) => {
    const term = searchQuery.toLowerCase().trim()
    if (!term) return true
    return (
      node.node_code.toLowerCase().includes(term) ||
      node.name.toLowerCase().includes(term)
    )
  })

  // Phân trang
  const totalItems = filteredNodes.length
  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize))
  const startIndex = totalItems === 0 ? 0 : (currentPage - 1) * pageSize + 1
  const endIndex = Math.min(currentPage * pageSize, totalItems)

  const paginatedNodes = filteredNodes.slice(
    (currentPage - 1) * pageSize,
    currentPage * pageSize
  )

  // Tính tổng quan tài nguyên cho Zone được chọn (KPI section)
  const totalCpu = nodes.reduce((acc, n) => acc + n.cpu_cores_total, 0)
  const usedCpu = nodes.reduce((acc, n) => acc + n.cpu_cores_used, 0)
  const totalRam = nodes.reduce((acc, n) => acc + n.ram_mb_total, 0)
  const usedRam = nodes.reduce((acc, n) => acc + n.ram_mb_used, 0)
  const onlineCount = nodes.filter(n => n.status === 'connected').length

  return (
    <PageContent>
      {/* Breadcrumbs & Header */}
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-2">
          <nav className="flex items-center gap-2 text-sm font-semibold text-muted-foreground">
            <span className="text-muted-foreground/80 hover:text-foreground cursor-pointer">Hypervisor</span>
            <span>/</span>
            <span className="text-foreground">Nodes</span>
          </nav>
          <div>
            <h1 className="text-3xl font-bold tracking-tight text-foreground">Hypervisor Nodes</h1>
            <p className="mt-1 text-sm text-muted-foreground">
              Manage and monitor all nodes in your infrastructure
            </p>
          </div>
        </div>
      </div>

      {/* KPI Section */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {/* KPI: Total Nodes */}
        <div className="rounded-2xl border border-border/60 bg-card p-5 shadow-xs flex items-center justify-between">
          <div className="space-y-1">
            <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Total Nodes</p>
            <h3 className="text-3xl font-bold text-foreground">{loading ? '...' : nodes.length}</h3>
          </div>
          <div className="p-3 bg-primary/10 rounded-xl">
            <Server className="size-5 text-primary" />
          </div>
        </div>

        {/* KPI: Online Nodes */}
        <div className="rounded-2xl border border-border/60 bg-card p-5 shadow-xs flex items-center justify-between">
          <div className="space-y-1">
            <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Online Nodes</p>
            <h3 className="text-3xl font-bold text-emerald-600 dark:text-emerald-400">
              {loading ? '...' : onlineCount}
            </h3>
          </div>
          <div className="p-3 bg-emerald-500/10 rounded-xl">
            <Activity className="size-5 text-emerald-500" />
          </div>
        </div>

        {/* KPI: vCPU Allocated */}
        <div className="rounded-2xl border border-border/60 bg-card p-5 shadow-xs flex items-center justify-between">
          <div className="space-y-1">
            <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">vCPU Allocated</p>
            <h3 className="text-2xl font-bold text-foreground">
              {loading ? '...' : `${usedCpu} / ${totalCpu} Cores`}
            </h3>
          </div>
          <div className="p-3 bg-amber-500/10 rounded-xl">
            <Cpu className="size-5 text-amber-500" />
          </div>
        </div>

        {/* KPI: RAM Allocated */}
        <div className="rounded-2xl border border-border/60 bg-card p-5 shadow-xs flex items-center justify-between">
          <div className="space-y-1">
            <p className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">RAM Allocated</p>
            <h3 className="text-2xl font-bold text-foreground">
              {loading ? '...' : `${(usedRam / 1024).toFixed(0)} / ${(totalRam / 1024).toFixed(0)} GB`}
            </h3>
          </div>
          <div className="p-3 bg-purple-500/10 rounded-xl">
            <Database className="size-5 text-purple-500" />
          </div>
        </div>
      </div>

      {/* Main Console Box */}
      <div className="space-y-5 rounded-2xl border border-border/60 bg-card p-6 shadow-sm">
        {/* Filter bar */}
        <div className="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div className="flex flex-col gap-3 md:flex-row md:items-center">
            {/* Zone Selector */}
            <div className="flex items-center gap-2">
              <span className="text-sm font-semibold text-muted-foreground flex items-center gap-1">
                <MapPin className="size-4" /> Zone:
              </span>
              <Select
                value={selectedZoneId}
                onValueChange={(val) => {
                  setSelectedZoneId(val)
                }}
              >
                <SelectTrigger className="h-10 w-52 rounded-lg border-border/60 bg-background text-sm font-medium">
                  <SelectValue placeholder="Select zone..." />
                </SelectTrigger>
                <SelectContent className="border-border/60 bg-background">
                  {zones.map((z) => (
                    <SelectItem key={z.id} value={z.id} className="text-sm font-medium">
                      {z.code.toUpperCase()} · {z.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {/* Search Input */}
            <div className="relative min-w-0 md:w-80">
              <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                placeholder="Search by node code or name..."
                className="h-10 rounded-lg border-border/60 bg-background pl-10 text-sm shadow-none focus-visible:ring-1"
              />
            </div>
          </div>

          <div className="flex items-center gap-2">
            {/* Action Buttons */}
            <Button
              variant="outline"
              size="icon"
              className="h-10 w-10 rounded-lg border-border/60"
              onClick={handleRefresh}
              disabled={loading || !selectedZoneId}
            >
              <RefreshCw className={cn('size-4 text-muted-foreground', (loading || refreshing) && 'animate-spin')} />
            </Button>
            <Button
              variant="outline"
              className="h-10 rounded-lg gap-2 text-sm font-semibold border-border/60"
              disabled={nodes.length === 0}
            >
              <FileDown className="size-4 text-muted-foreground" />
              Export
            </Button>
          </div>
        </div>

        {error && (
          <div className="rounded-lg border border-red-200 bg-red-500/10 px-4 py-3 text-sm font-semibold text-red-600 dark:text-red-400">
            {error}
          </div>
        )}

        {/* Data Table */}
        <div className="overflow-hidden rounded-xl border border-border/60">
          <Table>
            <TableHeader className="bg-muted/40">
              <TableRow className="border-b border-border/60">
                <TableHead className="text-xs font-bold uppercase tracking-wider text-muted-foreground h-11">Node Code</TableHead>
                <TableHead className="text-xs font-bold uppercase tracking-wider text-muted-foreground h-11">Name</TableHead>
                <TableHead className="text-xs font-bold uppercase tracking-wider text-muted-foreground h-11">Status</TableHead>
                <TableHead className="w-[160px] text-xs font-bold uppercase tracking-wider text-muted-foreground h-11">CPU (Used / Total)</TableHead>
                <TableHead className="w-[160px] text-xs font-bold uppercase tracking-wider text-muted-foreground h-11">RAM (Used / Total)</TableHead>
                <TableHead className="w-[160px] text-xs font-bold uppercase tracking-wider text-muted-foreground h-11">Storage (Used / Total)</TableHead>
                <TableHead className="text-xs font-bold uppercase tracking-wider text-muted-foreground h-11">Last Active</TableHead>
                <TableHead className="text-xs font-bold uppercase tracking-wider text-muted-foreground h-11">Created At</TableHead>
                <TableHead className="w-[48px] h-11" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading &&
                Array.from({ length: 5 }).map((_, index) => (
                  <TableRow key={index} className="border-b border-border/50">
                    <TableCell><Skeleton className="h-5 w-28 rounded" /></TableCell>
                    <TableCell><Skeleton className="h-5 w-36 rounded" /></TableCell>
                    <TableCell><Skeleton className="h-6 w-16 rounded-full" /></TableCell>
                    <TableCell>
                      <div className="space-y-1.5">
                        <Skeleton className="h-3 w-16 rounded" />
                        <Skeleton className="h-1.5 w-full rounded" />
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="space-y-1.5">
                        <Skeleton className="h-3 w-20 rounded" />
                        <Skeleton className="h-1.5 w-full rounded" />
                      </div>
                    </TableCell>
                    <TableCell>
                      <div className="space-y-1.5">
                        <Skeleton className="h-3 w-16 rounded" />
                        <Skeleton className="h-1.5 w-full rounded" />
                      </div>
                    </TableCell>
                    <TableCell><Skeleton className="h-5 w-24 rounded" /></TableCell>
                    <TableCell><Skeleton className="h-5 w-24 rounded" /></TableCell>
                    <TableCell><Skeleton className="size-8 rounded-full" /></TableCell>
                  </TableRow>
                ))}

              {!loading && !selectedZoneId && (
                <TableRow>
                  <TableCell colSpan={9} className="h-32 text-center text-sm font-medium text-muted-foreground">
                    Please select a zone to load hypervisor nodes.
                  </TableCell>
                </TableRow>
              )}

              {!loading && selectedZoneId && paginatedNodes.length === 0 && (
                <TableRow>
                  <TableCell colSpan={9} className="h-32 text-center text-sm font-medium text-muted-foreground">
                    No nodes found matching search filters.
                  </TableCell>
                </TableRow>
              )}

              {!loading &&
                paginatedNodes.map((item) => {
                  const cpuPercent = item.cpu_cores_total > 0 ? (item.cpu_cores_used / item.cpu_cores_total) * 100 : 0
                  const ramPercent = item.ram_mb_total > 0 ? (item.ram_mb_used / item.ram_mb_total) * 100 : 0
                  const storagePercent = item.storage_gb_total > 0 ? (item.storage_gb_used / item.storage_gb_total) * 100 : 0
                  const nodeActive = isNodeActive(item.last_active_at)

                  return (
                    <TableRow key={item.id} className="border-b border-border/50 hover:bg-muted/20">
                      {/* Node Code Link (ID is removed as requested) */}
                      <TableCell className="font-semibold text-foreground">
                        <Link
                          to="/hypervisor/$agentId"
                          params={{ agentId: item.id }}
                          className="hover:text-primary hover:underline transition-all"
                        >
                          {item.node_code.toUpperCase()}
                        </Link>
                      </TableCell>
                      
                      {/* Name */}
                      <TableCell className="text-sm font-medium text-muted-foreground">
                        {item.name}
                      </TableCell>

                      {/* Status */}
                      <TableCell>
                        <div className="flex items-center gap-1.5 text-xs font-semibold">
                          <span className={cn(
                            "size-2 rounded-full shrink-0 animate-pulse",
                            item.status === 'connected' && 'bg-emerald-500',
                            item.status === 'degraded' && 'bg-amber-500',
                            item.status === 'disconnected' && 'bg-red-500',
                            item.status === 'maintenance' && 'bg-purple-500'
                          )} />
                          <span className={cn(
                            item.status === 'connected' && 'text-emerald-600 dark:text-emerald-400',
                            item.status === 'degraded' && 'text-amber-600 dark:text-amber-400',
                            item.status === 'disconnected' && 'text-red-600 dark:text-red-400',
                            item.status === 'maintenance' && 'text-purple-600 dark:text-purple-400'
                          )}>
                            {item.status === 'connected' ? 'Online' : item.status === 'disconnected' ? 'Offline' : item.status}
                          </span>
                        </div>
                      </TableCell>

                      {/* CPU Allocated / Total */}
                      <TableCell className="py-3">
                        <div className="space-y-1 text-xs">
                          <div className="font-medium text-foreground flex items-center justify-between">
                            <span>{item.cpu_cores_used} / {item.cpu_cores_total} cores</span>
                          </div>
                          <Progress
                            value={cpuPercent}
                            className="h-1.5 w-full bg-muted/80"
                            indicatorClassName={cn(
                              cpuPercent >= 90 ? 'bg-destructive' : cpuPercent >= 70 ? 'bg-amber-500' : 'bg-emerald-500'
                            )}
                          />
                        </div>
                      </TableCell>

                      {/* RAM Allocated / Total */}
                      <TableCell className="py-3">
                        <div className="space-y-1 text-xs">
                          <div className="font-medium text-foreground flex items-center justify-between">
                            <span>
                              {item.ram_mb_used.toLocaleString('en-US')} / {item.ram_mb_total.toLocaleString('en-US')} MB
                            </span>
                          </div>
                          <Progress
                            value={ramPercent}
                            className="h-1.5 w-full bg-muted/80"
                            indicatorClassName={cn(
                              ramPercent >= 90 ? 'bg-destructive' : ramPercent >= 70 ? 'bg-amber-500' : 'bg-emerald-500'
                            )}
                          />
                        </div>
                      </TableCell>

                      {/* Storage Allocated / Total */}
                      <TableCell className="py-3">
                        <div className="space-y-1 text-xs">
                          <div className="font-medium text-foreground flex items-center justify-between">
                            <span>
                              {item.storage_gb_used.toLocaleString('en-US')} / {item.storage_gb_total.toLocaleString('en-US')} GB
                            </span>
                          </div>
                          <Progress
                            value={storagePercent}
                            className="h-1.5 w-full bg-muted/80"
                            indicatorClassName={cn(
                              storagePercent >= 90 ? 'bg-destructive' : storagePercent >= 70 ? 'bg-amber-500' : 'bg-emerald-500'
                            )}
                          />
                        </div>
                      </TableCell>

                      {/* Last Active */}
                      <TableCell>
                        <div className="flex items-center gap-1.5 text-xs font-semibold">
                          <span className={cn(
                            "size-1.5 rounded-full shrink-0",
                            nodeActive ? 'bg-emerald-500' : 'bg-red-500'
                          )} />
                          <span className={cn(
                            nodeActive ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400'
                          )}>
                            {formatLastActive(item.last_active_at)}
                          </span>
                        </div>
                      </TableCell>

                      {/* Created At */}
                      <TableCell className="text-sm font-medium text-muted-foreground">
                        {formatDate(item.created_at)}
                      </TableCell>

                      {/* Actions ellipsis */}
                      <TableCell>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button variant="ghost" size="icon" className="size-8 rounded-lg border-0 shadow-none hover:bg-muted">
                              <MoreVertical className="size-4 text-muted-foreground" />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end" className="border-border/60 bg-background">
                            <DropdownMenuItem className="text-sm font-medium">
                              <Link to="/hypervisor/$agentId" params={{ agentId: item.id }} className="w-full">
                                View details
                              </Link>
                            </DropdownMenuItem>
                            <DropdownMenuItem className="text-sm font-medium">Sync metrics</DropdownMenuItem>
                            <DropdownMenuItem className="text-sm font-medium text-destructive">Power actions</DropdownMenuItem>
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </TableCell>
                    </TableRow>
                  )
                })}
            </TableBody>
          </Table>
        </div>

        {/* Pagination controls */}
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <p className="text-xs text-muted-foreground">
            {totalItems === 0
              ? 'Showing 0 results'
              : `Showing ${startIndex} to ${endIndex} of ${totalItems} results`}
          </p>

          <div className="flex items-center gap-3">
            {/* Page Size select */}
            <div className="flex items-center gap-1.5">
              <span className="text-xs text-muted-foreground font-semibold">Rows per page:</span>
              <Select
                value={String(pageSize)}
                onValueChange={(val) => {
                  setPageSize(Number(val))
                  setCurrentPage(1)
                }}
              >
                <SelectTrigger className="h-9 w-20 rounded-lg border-border/60 bg-background text-xs font-semibold shadow-none">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent className="border-border/60 bg-background">
                  <SelectItem value="5" className="text-xs">5 / page</SelectItem>
                  <SelectItem value="10" className="text-xs">10 / page</SelectItem>
                  <SelectItem value="20" className="text-xs">20 / page</SelectItem>
                  <SelectItem value="50" className="text-xs">50 / page</SelectItem>
                </SelectContent>
              </Select>
            </div>

            {/* Prev/Next navigation */}
            <div className="flex items-center gap-1">
              <Button
                variant="outline"
                size="icon"
                className="h-9 w-9 rounded-lg border-border/60 shadow-none"
                onClick={() => setCurrentPage(prev => Math.max(1, prev - 1))}
                disabled={currentPage <= 1 || loading}
              >
                <ChevronLeft className="size-4 text-muted-foreground" />
              </Button>
              <span className="text-xs text-muted-foreground font-semibold px-2">
                Page {currentPage} of {totalPages}
              </span>
              <Button
                variant="outline"
                size="icon"
                className="h-9 w-9 rounded-lg border-border/60 shadow-none"
                onClick={() => setCurrentPage(prev => Math.min(totalPages, prev + 1))}
                disabled={currentPage >= totalPages || loading}
              >
                <ChevronRight className="size-4 text-muted-foreground" />
              </Button>
            </div>
          </div>
        </div>
      </div>
    </PageContent>
  )
}
