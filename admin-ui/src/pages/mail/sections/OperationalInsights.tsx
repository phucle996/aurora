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

type Insight = {
  title: string   // Tiêu đề của phân tích sâu (Ví dụ: "Warmup Complete")
  value: string   // Kết quả/Trạng thái định lượng
  note: string    // Lời khuyên cụ thể cho đội ngũ SRE
  tone: string    // Tông màu sắc cảnh báo đại diện (green, amber, blue)
}

interface OperationalInsightsProps {
  insights: Insight[]
}

/**
 * Component hiển thị các thông tin phân tích vận hành tự động (Operational Insights).
 * Phân tích hiệu suất hệ thống Mail giúp các quản trị viên đưa ra quyết định cải thiện hoặc nâng cấp phù hợp.
 */
export function OperationalInsights({ insights }: OperationalInsightsProps) {
  return (
    <Panel title="Operational Insights">
      {insights.length === 0 ? (
        /* Trạng thái trống khi chưa có đủ dữ liệu lịch sử để hệ thống phân tích sâu */
        <EmptyState
          title="No insights yet"
          description="Insights are generated after delivery attempts or resource status changes exist."
        />
      ) : (
        /* Danh sách các chỉ số phân tích vận hành sâu */
        <div className="divide-y divide-border/70">
          {insights.map((row, i) => (
            <div key={row.title || i} className="flex items-center gap-3 py-3 hover:bg-muted/10 px-1 rounded-lg transition-colors">
              {/* Xác định tông màu sắc icon theo thông số tone được hệ thống AI phân tích gửi về */}
              <span
                className={cn(
                  'inline-flex size-9 items-center justify-center rounded-full shrink-0',
                  row.tone === 'green'
                    ? 'bg-emerald-500/10 text-emerald-600'
                    : row.tone === 'amber'
                      ? 'bg-amber-500/10 text-amber-600'
                      : 'bg-primary/10 text-primary'
                )}
              >
                {/* Lựa chọn Icon phù hợp với phân loại tone màu sắc */}
                {row.tone === 'green' ? (
                  <ShieldCheck className="size-4" />
                ) : row.tone === 'amber' ? (
                  <AlertTriangle className="size-4" />
                ) : (
                  <Zap className="size-4" />
                )}
              </span>
              
              <div className="min-w-0 flex-1">
                {/* Tiêu đề phân tích */}
                <p className="aurora-insight-title font-semibold truncate">{row.title}</p>
                {/* Giá trị và ghi chú cụ thể */}
                <p className="text-xs font-medium text-muted-foreground mt-0.5">
                  {row.value} — <span className="font-semibold text-foreground/80">{row.note}</span>
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
