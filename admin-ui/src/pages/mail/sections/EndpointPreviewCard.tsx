import { Zap } from 'lucide-react'
import { Badge } from '@/components/ui/badge'

interface EndpointPreviewCardProps {
  name: string
  host: string
  port: number
  tlsMode: string
  priority: number
  weight: number
  maxConnections: number
  username: string
}

export function EndpointPreviewCard({
  name,
  host,
  port,
  tlsMode,
  priority,
  weight,
  maxConnections,
  username,
}: EndpointPreviewCardProps) {
  return (
    <div className="space-y-6">
      <div className="sticky top-6 rounded-xl border border-border bg-card p-6 shadow-xs">
        <h2 className="text-xl font-semibold tracking-[-0.02em] text-foreground">Live Preview</h2>
        <p className="mt-1 text-sm text-muted-foreground">Real-time status of the new mail endpoint.</p>

        <div className="mt-6 space-y-4 rounded-lg border border-border/50 bg-muted/20 p-5">
          <div>
            <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Endpoint Name</span>
            <p className="text-base font-bold text-foreground mt-0.5">{name || 'Unnamed Endpoint'}</p>
          </div>

          <div>
            <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">SMTP Destination</span>
            <p className="text-sm font-medium text-foreground mt-0.5">
              {host ? `${host}:${port}` : 'No host address set'}
            </p>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">TLS Mode</span>
              <p className="text-sm font-medium text-foreground mt-0.5">
                {tlsMode.toUpperCase()}
              </p>
            </div>
            <div>
              <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Status</span>
              <div className="mt-1">
                <Badge variant="outline" className="font-medium text-xs rounded-md px-2 py-0.5 border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-500/20 dark:bg-blue-500/10 dark:text-blue-400">
                  Planned
                </Badge>
              </div>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div>
              <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Priority / Weight</span>
              <p className="text-sm font-medium text-foreground mt-0.5">{priority} / {weight}</p>
            </div>
            <div>
              <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Concurrency Pool</span>
              <p className="text-sm font-medium text-foreground mt-0.5">{maxConnections} connections</p>
            </div>
          </div>

          <div>
            <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Credentials</span>
            <p className="text-sm font-medium text-foreground mt-0.5">
              {username ? `Auth with user: ${username}` : 'No username set'}
            </p>
          </div>
        </div>

        <div className="mt-6 flex flex-col gap-2 rounded-lg bg-primary/5 p-4 border border-primary/10">
          <div className="flex gap-2">
            <Zap className="size-4 text-primary shrink-0 mt-0.5" />
            <p className="text-xs text-primary leading-relaxed">
              <span className="font-semibold">SRE Tip:</span> Bạn có thể click <span className="font-semibold">"Try Connect"</span> để kiểm tra khả năng bắt tay SMTP & TLS của endpoint trước khi lưu chính thức vào cơ sở dữ liệu.
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}
