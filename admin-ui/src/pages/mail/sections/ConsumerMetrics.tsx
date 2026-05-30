import { ArrowDown, ArrowUp, Users, ShieldCheck, Activity, Clock3, Target } from 'lucide-react'
import { cn } from '@/lib/utils'

// Định nghĩa các loại tone màu sắc khác nhau để hiển thị cho các card metrics
type Tone = 'blue' | 'green' | 'purple' | 'amber' | 'red' | 'slate'

// Ánh xạ các tone màu tới CSS classes Tailwind giúp giao diện đồng nhất và trực quan
const toneClass: Record<Tone, string> = {
  blue: 'bg-primary/10 text-primary',
  green: 'bg-emerald-500/10 text-emerald-600',
  purple: 'bg-violet-500/10 text-violet-600',
  amber: 'bg-amber-500/10 text-amber-600',
  red: 'bg-destructive/10 text-destructive',
  slate: 'bg-slate-500/10 text-muted-foreground',
}

// Định dạng số nguyên sang chuẩn chuỗi định dạng quốc tế (ví dụ: 1,000)
function formatNumber(value: number) {
  return new Intl.NumberFormat('en-US').format(value)
}

// Định dạng số dạng rút gọn (ví dụ: 1.2K, 3.4M) giúp tối ưu diện tích hiển thị trên các màn hình nhỏ
function formatCompactNumber(value: number) {
  return new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 2 }).format(value)
}

type Metric = {
  label: string         // Nhãn hiển thị chỉ số
  value: string         // Giá trị định lượng chính
  sub?: string          // Ghi chú phụ bổ sung
  delta?: string        // Chỉ số biến động (nếu có)
  trend: 'up' | 'down'  // Hướng tăng hoặc giảm
  icon: React.ReactNode // Icon từ lucide-react đại diện
  tone: Tone            // Tông màu sắc thẻ
}

/**
 * Component hiển thị một thẻ chỉ số đơn lẻ cho Consumers
 */
function MetricCard({ label, value, sub, delta, trend, icon, tone }: Metric) {
  const TrendIcon = trend === 'up' ? ArrowUp : ArrowDown
  return (
    <section className="rounded-xl border border-border bg-card p-4 shadow-sm hover:shadow transition-all duration-300">
      <div className="flex items-center gap-3">
        <span className={cn('inline-flex size-12 shrink-0 items-center justify-center rounded-full', toneClass[tone])}>
          {icon}
        </span>
        <div className="min-w-0 flex-1">
          <p className="aurora-card-label">{label}</p>
          <p className="mt-1 aurora-metric-value">{value}</p>
          {sub ? <p className="aurora-caption mt-0.5">{sub}</p> : null}
        </div>
      </div>
      {delta ? (
        <div className="mt-3 flex items-center gap-1 text-xs font-semibold">
          <TrendIcon className={cn('size-3.5', trend === 'up' ? 'text-emerald-600' : 'text-destructive')} />
          <span className={trend === 'up' ? 'text-emerald-600' : 'text-destructive'}>{delta}</span>
          <span className="font-medium text-muted-foreground">live DB</span>
        </div>
      ) : (
        <div className="mt-3 text-xs font-medium text-muted-foreground">live DB</div>
      )}
    </section>
  )
}

interface ConsumerMetricsProps {
  metrics: {
    total_consumers: number        // Tổng số consumers đã đăng ký trong hệ thống
    active_consumers: number       // Số consumers đang thực thi nhiệm vụ
    disabled_consumers: number     // Số consumers đang tạm dừng hoặc cấu hình disable
    total_lag: number              // Độ trễ (lag) thông điệp chưa kịp xử lý
    avg_worker_concurrency: number // Độ song song thực thi trung bình
    avg_ack_timeout_seconds: number // Thời gian chờ xác nhận phản hồi trung bình
  }
  tenantCount: number              // Số lượng tổ chức đang dùng
}

/**
 * Component lưới hiển thị các thẻ metrics đo lường hiệu suất thực thi của các Consumers.
 * Giúp đội ngũ phát hiện hiện tượng nghẽn mạng (high consumer lag) hoặc quá tải công việc (worker concurrency).
 */
export function ConsumerMetrics({ metrics, tenantCount }: ConsumerMetricsProps) {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-6">
      {/* 1. Tổng số Consumers */}
      <MetricCard
        label="Total Consumers"
        value={formatNumber(metrics.total_consumers)}
        trend="up"
        icon={<Users className="size-5" />}
        tone="blue"
      />
      {/* 2. Số lượng Consumer đang Active / Disabled */}
      <MetricCard
        label="Active Consumers"
        value={formatNumber(metrics.active_consumers)}
        sub={`${formatNumber(metrics.disabled_consumers)} disabled`}
        trend="up"
        icon={<ShieldCheck className="size-5" />}
        tone="green"
      />
      {/* 3. Độ song song xử lý trung bình của workers */}
      <MetricCard
        label="Avg Worker Concurrency"
        value={metrics.avg_worker_concurrency.toFixed(1)}
        trend="up"
        icon={<Activity className="size-5" />}
        tone="purple"
      />
      {/* 4. Tổng độ trễ tin nhắn (Consumer Lag) trong các queue */}
      <MetricCard
        label="Consumer Lag Queue"
        value={formatCompactNumber(metrics.total_lag)}
        sub="msgs"
        trend="down"
        icon={<Clock3 className="size-5" />}
        tone="amber"
      />
      {/* 5. Thời gian chờ phản hồi tối đa trung bình */}
      <MetricCard
        label="Avg Ack Timeout"
        value={`${metrics.avg_ack_timeout_seconds.toFixed(1)}s`}
        trend="down"
        icon={<Clock3 className="size-5" />}
        tone="red"
      />
      {/* 6. Số lượng tổ chức được định tuyến */}
      <MetricCard
        label="Organizations"
        value={formatNumber(tenantCount)}
        sub="with tenant_id"
        trend="up"
        icon={<Target className="size-5" />}
        tone="blue"
      />
    </div>
  )
}
