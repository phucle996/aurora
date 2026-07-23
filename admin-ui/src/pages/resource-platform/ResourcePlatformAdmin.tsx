import { useCallback, useEffect, useState } from 'react'
import {
  Boxes,
  Layers,
  ListChecks,
  RefreshCcw,
  Server,
} from 'lucide-react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { usePageMeta } from '@/lib/page-meta'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { cn } from '@/lib/utils'
import {
  resourcePlatformAdminApi,
  type RPDefinition,
  type RPModel,
  type RPTemplate,
  type RPCluster,
  type RPJob,
  type RPJobLog,
} from '@/lib/resource-platform-api'

type TabKey = 'catalog' | 'templates' | 'clusters' | 'jobs'

const tabs: { key: TabKey; label: string; icon: typeof Boxes }[] = [
  { key: 'catalog', label: 'Catalog', icon: Boxes },
  { key: 'templates', label: 'Templates', icon: Layers },
  { key: 'clusters', label: 'Clusters', icon: Server },
  { key: 'jobs', label: 'Jobs', icon: ListChecks },
]

const statusCls: Record<string, string> = {
  active: 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400',
  published: 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400',
  draft: 'border-amber-200 bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-400',
  deprecated: 'border-slate-200 bg-slate-50 text-slate-600 dark:bg-slate-500/10 dark:text-slate-400',
  queued: 'border-blue-200 bg-blue-50 text-blue-700 dark:bg-blue-500/10 dark:text-blue-400',
  running: 'border-violet-200 bg-violet-50 text-violet-700 dark:bg-violet-500/10 dark:text-violet-400',
  succeeded: 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400',
  failed: 'border-red-200 bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-400',
}

function Panel({ title, children, action }: { title: string; children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <div className="rounded-2xl border border-border bg-card shadow-xs">
      <div className="flex items-center justify-between px-5 py-4 border-b border-border/60">
        <h3 className="text-sm font-bold uppercase tracking-wider text-muted-foreground/60">{title}</h3>
        {action}
      </div>
      <div className="p-5">{children}</div>
    </div>
  )
}


function PanelSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div className="rounded-2xl border border-border bg-card shadow-xs">
      <div className="flex items-center justify-between border-b border-border/60 px-5 py-4">
        <Skeleton className="h-4 w-36" />
        <Skeleton className="h-8 w-24 rounded-lg" />
      </div>
      <div className="space-y-3 p-5">
        {Array.from({ length: rows }).map((_, index) => (
          <Skeleton key={index} className="h-12 w-full rounded-xl" />
        ))}
      </div>
    </div>
  )
}

function TableSkeleton({ columns = 4, rows = 5 }: { columns?: number; rows?: number }) {
  return (
    <div className="rounded-2xl border border-border bg-card shadow-xs">
      <div className="flex items-center justify-between border-b border-border/60 px-5 py-4">
        <Skeleton className="h-4 w-40" />
        <Skeleton className="h-8 w-24 rounded-lg" />
      </div>
      <div className="space-y-3 p-5">
        {Array.from({ length: rows }).map((_, rowIndex) => (
          <div key={rowIndex} className="grid gap-3" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>
            {Array.from({ length: columns }).map((__, colIndex) => (
              <Skeleton key={colIndex} className="h-10 w-full rounded-lg" />
            ))}
          </div>
        ))}
      </div>
    </div>
  )
}

function formatRelativeTime(dateStr: string): string {
  const diff = Date.now() - new Date(dateStr).getTime()
  if (diff < 60000) return 'Just now'
  const m = Math.floor(diff / 60000)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}

function CatalogTab() {
  const [definitions, setDefinitions] = useState<RPDefinition[]>([])
  const [models, setModels] = useState<Record<string, RPModel[]>>({})
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    void (async () => {
      try {
        const defs = await resourcePlatformAdminApi.listDefinitions()
        setDefinitions(defs ?? [])
        const modelMap: Record<string, RPModel[]> = {}
        for (const def of defs ?? []) {
          try {
            modelMap[def.id] = (await resourcePlatformAdminApi.listModels(def.id)) ?? []
          } catch { modelMap[def.id] = [] }
        }
        setModels(modelMap)
      } catch { /* ignore */ } finally { setLoading(false) }
    })()
  }, [])

  if (loading) return <PanelSkeleton rows={5} />

  return (
    <div className="space-y-6">
      {definitions.map((def) => (
        <Panel key={def.id} title={def.name}>
          <p className="text-sm text-muted-foreground mb-4">{def.description || 'No description'}</p>
          <div className="flex items-center gap-2 mb-4">
            <Badge variant="outline" className={cn('text-xs font-bold', statusCls[def.status])}>{def.status}</Badge>
            <span className="text-xs text-muted-foreground">Category: {def.category || '—'}</span>
          </div>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Model</TableHead>
                <TableHead>Slug</TableHead>
                <TableHead>Engine</TableHead>
                <TableHead>Status</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(models[def.id] ?? []).length === 0 ? (
                <TableRow><TableCell colSpan={4} className="text-center py-6 text-muted-foreground">No models</TableCell></TableRow>
              ) : (models[def.id] ?? []).map((model) => (
                <TableRow key={model.id}>
                  <TableCell className="font-bold">{model.name}</TableCell>
                  <TableCell className="text-xs font-mono text-muted-foreground">{model.slug}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{model.engine || '—'}</TableCell>
                  <TableCell><Badge variant="outline" className={cn('text-xs font-bold', statusCls[model.status])}>{model.status}</Badge></TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Panel>
      ))}
      {definitions.length === 0 && <div className="py-8 text-center text-muted-foreground">No definitions found</div>}
    </div>
  )
}

function TemplatesTab() {
  const [templates, setTemplates] = useState<RPTemplate[]>([])
  const [loading, setLoading] = useState(true)
  useEffect(() => { resourcePlatformAdminApi.listTemplates().then((t) => setTemplates(t ?? [])).catch(() => {}).finally(() => setLoading(false)) }, [])

  if (loading) return <TableSkeleton columns={4} rows={4} />

  return (
    <Panel title="Resource Templates">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead><TableHead>Slug</TableHead><TableHead>Type</TableHead><TableHead>Status</TableHead><TableHead>Default</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {templates.length === 0 ? (
            <TableRow><TableCell colSpan={5} className="text-center py-6 text-muted-foreground">No templates</TableCell></TableRow>
          ) : templates.map((t) => (
            <TableRow key={t.id}>
              <TableCell className="font-bold">{t.name}</TableCell>
              <TableCell className="text-xs font-mono text-muted-foreground">{t.slug}</TableCell>
              <TableCell className="text-xs text-muted-foreground capitalize">{t.template_type || '—'}</TableCell>
              <TableCell><Badge variant="outline" className={cn('text-xs font-bold', statusCls[t.status])}>{t.status}</Badge></TableCell>
              <TableCell>{t.is_default ? '✓' : '—'}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Panel>
  )
}

function ClustersTab() {
  const [clusters, setClusters] = useState<RPCluster[]>([])
  const [loading, setLoading] = useState(true)
  useEffect(() => { resourcePlatformAdminApi.listClusters().then((c) => setClusters(c ?? [])).catch(() => {}).finally(() => setLoading(false)) }, [])

  if (loading) return <TableSkeleton columns={4} rows={4} />

  return (
    <Panel title="Clusters">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead><TableHead>Region</TableHead><TableHead>Environment</TableHead><TableHead>Endpoint</TableHead><TableHead>Status</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {clusters.length === 0 ? (
            <TableRow><TableCell colSpan={5} className="text-center py-6 text-muted-foreground">No clusters</TableCell></TableRow>
          ) : clusters.map((c) => (
            <TableRow key={c.id}>
              <TableCell className="font-bold">{c.name}</TableCell>
              <TableCell className="text-xs text-muted-foreground">{c.region}</TableCell>
              <TableCell className="text-xs text-muted-foreground capitalize">{c.environment}</TableCell>
              <TableCell className="text-xs font-mono text-muted-foreground max-w-[200px] truncate">{c.endpoint || '—'}</TableCell>
              <TableCell><Badge variant="outline" className={cn('text-xs font-bold', statusCls[c.status])}>{c.status}</Badge></TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Panel>
  )
}

function JobsTab() {
  const [jobs, setJobs] = useState<RPJob[]>([])
  const [loading, setLoading] = useState(true)
  const [selectedJob, setSelectedJob] = useState<RPJob | null>(null)
  const [logs, setLogs] = useState<RPJobLog[]>([])
  const [logsLoading, setLogsLoading] = useState(false)

  const refresh = useCallback(() => {
    setLoading(true)
    resourcePlatformAdminApi.listJobs().then((j) => setJobs(j ?? [])).catch(() => {}).finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    let ignore = false
    resourcePlatformAdminApi
      .listJobs()
      .then((j) => {
        if (!ignore) setJobs(j ?? [])
      })
      .catch(() => {})
      .finally(() => {
        if (!ignore) setLoading(false)
      })
    return () => {
      ignore = true
    }
  }, [])

  const showLogs = async (job: RPJob) => {
    setSelectedJob(job)
    setLogsLoading(true)
    try {
      const l = await resourcePlatformAdminApi.getJobLogs(job.id)
      setLogs(l ?? [])
    } catch { setLogs([]) } finally { setLogsLoading(false) }
  }

  return (
    <div className="space-y-4">
      <Panel title="Resource Jobs" action={<Button variant="outline" size="sm" onClick={refresh} disabled={loading}><RefreshCcw className={cn('size-3.5 mr-1.5', loading && 'animate-spin')} />Refresh</Button>}>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead><TableHead>Type</TableHead><TableHead>Status</TableHead><TableHead>Retries</TableHead><TableHead>Error</TableHead><TableHead>Updated</TableHead><TableHead>Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 5 }).map((_, index) => (
                <TableRow key={`job-sk-${index}`}>
                  <TableCell colSpan={7}><Skeleton className="h-10 rounded-lg" /></TableCell></TableRow>
              ))
            ) : jobs.length === 0 ? (
              <TableRow><TableCell colSpan={7} className="text-center py-6 text-muted-foreground">No jobs</TableCell></TableRow>
            ) : jobs.map((j) => (
              <TableRow key={j.id} className={cn(selectedJob?.id === j.id && 'bg-primary/5')}>
                <TableCell className="text-xs font-mono text-muted-foreground">{j.id.slice(0, 12)}...</TableCell>
                <TableCell className="text-xs font-bold capitalize">{j.job_type.replace(/_/g, ' ').toLowerCase()}</TableCell>
                <TableCell><Badge variant="outline" className={cn('text-xs font-bold', statusCls[j.status])}>{j.status}</Badge></TableCell>
                <TableCell className="text-xs text-muted-foreground">{j.retry_count}/{j.max_retries}</TableCell>
                <TableCell className="text-xs text-red-600 max-w-[200px] truncate">{j.error_message || '—'}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{formatRelativeTime(j.updated_at)}</TableCell>
                <TableCell>
                  <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={() => void showLogs(j)}>Logs</Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Panel>

      {selectedJob && (
        <Panel title={`Job Logs — ${selectedJob.id.slice(0, 16)}`}>
          {logsLoading ? (
            <div className="space-y-2">
              {Array.from({ length: 4 }).map((_, index) => <Skeleton key={index} className="h-16 w-full rounded-lg" />)}
            </div>
          ) : logs.length === 0 ? (
            <div className="py-4 text-center text-sm text-muted-foreground">No logs</div>
          ) : (
            <div className="space-y-2">
              {logs.map((log) => (
                <div key={log.id} className="flex items-start gap-3 rounded-lg border border-border/40 bg-muted/20 px-4 py-3">
                  <Badge variant="outline" className={cn('text-[10px] shrink-0', log.level === 'error' ? 'text-red-600' : log.level === 'warn' ? 'text-amber-600' : 'text-blue-600')}>{log.level}</Badge>
                  <div className="min-w-0 flex-1">
                    <p className="text-sm text-foreground">{log.message}</p>
                    <p className="text-[10px] text-muted-foreground mt-1">{new Date(log.created_at).toLocaleString()}</p>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Panel>
      )}
    </div>
  )
}

export default function ResourcePlatformAdmin() {
  usePageMeta('Resource Platform | Aurora Admin', 'Manage resource definitions, templates, clusters, and jobs.')
  const [activeTab, setActiveTab] = useState<TabKey>('catalog')

  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-1">
          <h1 className="m-0 text-[40px] font-black leading-none tracking-tight text-foreground md:text-[46px]">Resource Platform</h1>
          <p className="text-base font-medium text-muted-foreground">Manage catalog, templates, clusters, and provisioning jobs</p>
        </div>
      </div>

      <div className="flex flex-wrap gap-1.5 rounded-xl border border-border bg-card p-1.5 shadow-xs">
        {tabs.map((tab) => {
          const Icon = tab.icon
          return (
            <button
              key={tab.key}
              type="button"
              onClick={() => setActiveTab(tab.key)}
              className={cn(
                'inline-flex items-center gap-2 rounded-lg px-4 py-2.5 text-sm font-bold transition-colors',
                activeTab === tab.key ? 'bg-primary text-primary-foreground shadow-sm' : 'text-muted-foreground hover:bg-muted hover:text-foreground',
              )}
            >
              <Icon className="size-4" />
              {tab.label}
            </button>
          )
        })}
      </div>

      {activeTab === 'catalog' && <CatalogTab />}
      {activeTab === 'templates' && <TemplatesTab />}
      {activeTab === 'clusters' && <ClustersTab />}
      {activeTab === 'jobs' && <JobsTab />}
    </div>
  )
}
