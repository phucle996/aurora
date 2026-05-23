import { useCallback, useState } from 'react'
import { CheckCircle2, XCircle, AlertTriangle, Clock } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Skeleton } from '@/components/ui/skeleton'
import { Fetch } from '@/lib/fetch'
import { usePageMeta } from '@/lib/page-meta'
import { cn } from '@/lib/utils'

type Attempt = {
  id: string; consumer_id: string; template_id: string; gateway_id: string
  endpoint_id: string; message_id: string; subject: string; status: string
  error_message: string; error_class: string; retry_count: number
  trace_id: string; workspace_id: string; tenant_id: string; created_at: string
}

const statusCls: Record<string, string> = {
  delivered: 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400',
  failed: 'border-red-200 bg-red-50 text-red-700 dark:bg-red-500/10 dark:text-red-400',
  dead_letter: 'border-amber-200 bg-amber-50 text-amber-700 dark:bg-amber-500/10 dark:text-amber-400',
}
const statusIcon: Record<string, React.ReactNode> = {
  delivered: <CheckCircle2 className="size-3.5 text-emerald-500" />,
  failed: <XCircle className="size-3.5 text-red-500" />,
  dead_letter: <AlertTriangle className="size-3.5 text-amber-500" />,
  retried: <Clock className="size-3.5 text-blue-500" />,
}

function timeAgo(d: string) {
  const diff = Date.now() - new Date(d).getTime()
  if (diff < 60000) return 'Just now'
  const m = Math.floor(diff / 60000)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}

export default function DeliveryAttemptsPage() {
  usePageMeta('Delivery Attempts | Aurora Admin', 'Review SMTP delivery attempts and failure diagnostics.')
  const [attempts, setAttempts] = useState<Attempt[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true); setError('')
    try {
      const resp = await Fetch('/api/v1/runtime/delivery-attempts')
      if (!resp.ok) { setError('Failed to load'); return }
      const body = await resp.json()
      setAttempts(body.data?.items ?? body.items ?? [])
    } catch { setError('Failed to load') }
    finally { setLoading(false) }
  }, [])

  useState(() => { void load() })

  return (
    <>
      <div className="space-y-6 p-6">
        <div><h1 className="text-2xl font-bold">Delivery Attempts</h1><p className="text-sm text-muted-foreground">{loading ? 'Loading...' : `${attempts.length} attempts`}</p></div>
        <div className="rounded-xl border bg-card overflow-hidden">
          {error ? <div className="p-6"><div className="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">{error}</div></div> : (
            <Table>
              <TableHeader>
                <TableRow className="bg-muted/30 hover:bg-muted/30">
                  <TableHead className="text-xs font-bold uppercase">Status</TableHead>
                  <TableHead className="text-xs font-bold uppercase">Subject</TableHead>
                  <TableHead className="text-xs font-bold uppercase">Workspace</TableHead>
                  <TableHead className="text-xs font-bold uppercase">Error</TableHead>
                  <TableHead className="text-xs font-bold uppercase">Retries</TableHead>
                  <TableHead className="text-xs font-bold uppercase">Trace ID</TableHead>
                  <TableHead className="text-xs font-bold uppercase">Time</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {attempts.length === 0 && !loading ? (
                  <TableRow><TableCell colSpan={7} className="h-32 text-center text-muted-foreground">No delivery attempts</TableCell></TableRow>
                ) : attempts.map(a => (
                  <TableRow key={a.id} className="hover:bg-muted/20">
                    <TableCell><div className="flex items-center gap-1.5">{statusIcon[a.status]}<Badge variant="outline" className={cn('text-[10px] font-bold', statusCls[a.status])}>{a.status}</Badge></div></TableCell>
                    <TableCell className="text-sm max-w-[180px] truncate">{a.subject || '—'}</TableCell>
                    <TableCell className="text-[10px] font-mono text-muted-foreground">{a.workspace_id || '—'}</TableCell>
                    <TableCell className="text-xs text-muted-foreground max-w-[180px] truncate">{a.error_message || '—'}</TableCell>
                    <TableCell className="text-center font-bold">{a.retry_count}</TableCell>
                    <TableCell className="text-[10px] font-mono text-muted-foreground max-w-[100px] truncate">{a.trace_id || '—'}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">{timeAgo(a.created_at)}</TableCell>
                  </TableRow>
                ))}
                {loading && Array.from({ length: 5 }).map((_, i) => <TableRow key={`sk-${i}`}><TableCell colSpan={7}><Skeleton className="h-10 rounded-lg" /></TableCell></TableRow>)}
              </TableBody>
            </Table>
          )}
        </div>
      </div>
    </>
  )
}
