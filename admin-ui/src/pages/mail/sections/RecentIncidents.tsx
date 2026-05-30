import { type ReactNode } from 'react'
import { ChevronRight, Zap, ShieldCheck, AlertTriangle } from 'lucide-react'
import { cn } from '@/lib/utils'

/**
 * Component Panel bọc nội dung và tiêu đề
 */
function Panel({
  title,
  action,
  children,
  className,
}: {
  title: string
  action?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={cn('rounded-xl border border-border bg-card p-4 shadow-sm', className)}>
      <div className="mb-4 flex items-center justify-between gap-3">
        <h3 className="aurora-section-title">{title}</h3>
        {action}
      </div>
      {children}
    </section>
  )
}

/**
 * Component EmptyState hiển thị khi không có dữ liệu để xem
 */
function EmptyState({ title, description }: { title: string; description: string }) {
  return (
    <div className="rounded-lg border border-dashed border-border bg-muted/30 p-6 text-center">
      <p className="aurora-insight-title">{title}</p>
      <p className="mt-1 aurora-insight-meta">{description}</p>
    </div>
  )
}

// Tính toán thời gian tương đối so với hiện tại để hiển thị dễ hiểu (Ví dụ: "5m ago", "2h ago")
function formatRelativeTime(input: string) {
  const value = new Date(input).getTime()
  if (!Number.isFinite(value)) return ''
  const diff = Math.max(Date.now() - value, 0)
  const minutes = Math.floor(diff / 60000)
  if (minutes < 1) return 'just now'
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

type TimelineRow = {
  id: string          // Khóa chính của log dòng thời gian
  entity_type: string // Loại thực thể bị ảnh hưởng (Ví dụ: Endpoint, Gateway)
  entity_name: string // Tên cụ thể của tài nguyên
  action: string      // Thao tác sự kiện (Ví dụ: Create, Delete, Alert)
  actor_name: string  // Tên người thực hiện
  note: string        // Ghi chú chi tiết hoặc thông điệp lỗi kèm theo
  created_at: string  // Thời gian ghi nhận sự kiện
}

interface RecentIncidentsProps {
  timeline: TimelineRow[]
}

/**
 * Component hiển thị danh sách các sự cố hoặc sự thay đổi cấu hình gần đây nhất của Mail Fleet.
 * Hỗ trợ giao diện dòng thời gian trực quan, giúp phát hiện nhanh các thay đổi đột ngột làm ảnh hưởng hệ thống.
 */
export function RecentIncidents({ timeline }: RecentIncidentsProps) {
  return (
    <Panel
      title="Recent Incidents & Changes"
      action={<a className="aurora-link-text hover:underline cursor-pointer">View all</a>}
    >
      {timeline.length === 0 ? (
        /* Trạng thái trống khi không có thay đổi nào được ghi nhận */
        <EmptyState
          title="No recent changes"
          description="Activity logs will appear here when Mail resources change."
        />
      ) : (
        /* Danh sách các log dòng thời gian */
        <div className="divide-y divide-border/70">
          {timeline.map((row, i) => (
            <div key={row.id || i} className="flex items-center gap-3 py-3 hover:bg-muted/10 px-1 rounded-lg transition-colors">
              {/* Hiển thị icon tròn với màu sắc biến đổi theo vòng lặp để đa dạng hóa giao diện trực quan */}
              <span
                className={cn(
                  'inline-flex size-9 items-center justify-center rounded-full shrink-0',
                  ['bg-primary/10 text-primary', 'bg-emerald-500/10 text-emerald-600', 'bg-amber-500/10 text-amber-600', 'bg-violet-500/10 text-violet-600'][i % 4]
                )}
              >
                {i % 3 === 0 ? <Zap className="size-4" /> : i % 3 === 1 ? <ShieldCheck className="size-4" /> : <AlertTriangle className="size-4" />}
              </span>
              <div className="min-w-0 flex-1">
                {/* Tên tài nguyên hoặc loại tài nguyên bị thay đổi */}
                <p className="aurora-insight-title font-semibold truncate">{row.entity_name || row.entity_type}</p>
                {/* Thông tin chi tiết về hành động và thời gian xảy ra */}
                <p className="text-xs font-medium text-muted-foreground mt-0.5">
                  {row.action} — <span className="font-semibold text-foreground/80">{row.note || formatRelativeTime(row.created_at)}</span>
                </p>
              </div>
              <ChevronRight className="size-4 text-muted-foreground shrink-0" />
            </div>
          ))}
        </div>
      )}
    </Panel>
  )
}
