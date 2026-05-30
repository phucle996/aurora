import { ArrowDown, ArrowUp, Mail, Activity, Users, Server, Target } from 'lucide-react'
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

// Kiểu dữ liệu cấu trúc cho mỗi thẻ Metric
type Metric = {
  label: string         // Nhãn của chỉ số (Ví dụ: "Queue Depth")
  value: string         // Giá trị dạng chuỗi hiển thị
  sub?: string          // Ghi chú phụ phía dưới giá trị (tùy chọn)
  delta?: string        // Chỉ số biến động (tùy chọn)
  trend: 'up' | 'down'  // Hướng biến động (lên hoặc xuống)
  icon: React.ReactNode // Icon minh họa từ thư viện lucide-react
  tone: Tone            // Tông màu chủ đạo của thẻ
}

/**
 * Component hiển thị một thẻ chỉ số đơn lẻ (Metric Card)
 * Thiết kế theo chuẩn giao diện Aurora, hỗ trợ hiệu ứng hover mượt mà và hiển thị trạng thái dữ liệu trực tiếp từ cơ sở dữ liệu.
 */
function MetricCard({ label, value, sub, delta, trend, icon, tone }: Metric) {
  const TrendIcon = trend === 'up' ? ArrowUp : ArrowDown
  return (
    <section className="rounded-xl border border-border bg-card p-4 shadow-sm hover:shadow transition-all duration-300">
      <div className="flex items-center gap-3">
        {/* Vùng hiển thị Icon dạng tròn với tông màu tương ứng */}
        <span className={cn('inline-flex size-12 shrink-0 items-center justify-center rounded-full', toneClass[tone])}>
          {icon}
        </span>
        <div className="min-w-0 flex-1">
          {/* Nhãn và giá trị chỉ số chính */}
          <p className="aurora-card-label">{label}</p>
          <p className="mt-1 aurora-metric-value">{value}</p>
          {/* Nhãn phụ (nếu có) */}
          {sub ? <p className="aurora-caption mt-0.5">{sub}</p> : null}
        </div>
      </div>
      {/* Hiển thị tỷ lệ biến động delta hoặc chỉ báo trạng thái kết nối realtime */}
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

// Định nghĩa các props truyền vào cho OverviewMetrics tổng quan
interface OverviewMetricsProps {
  metrics: {
    delivered_today: number     // Số lượng email đã gửi thành công trong ngày
    queued_now: number          // Số lượng email đang đợi trong hàng đợi hệ thống
    active_consumers: number    // Số lượng consumers đang hoạt động
    total_consumers: number     // Tổng số lượng consumers đã được đăng ký
    active_gateways: number     // Số lượng gateways đang hoạt động
    total_gateways: number      // Tổng số lượng gateways cấu hình
    delivery_success_rate: number // Tỉ lệ gửi thư thành công (%)
  }
}

/**
 * Component hiển thị lưới các thẻ metrics tổng quan của hệ thống Mail Admin
 * Hỗ trợ hiển thị responsive tự động co giãn từ 1 cột trên mobile đến 5 cột trên màn hình desktop xl.
 */
export function OverviewMetrics({ metrics }: OverviewMetricsProps) {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-5">
      {/* Card 1: Số thư đã gửi thành công trong 24h qua */}
      <MetricCard
        label="Delivered (24h)"
        value={formatCompactNumber(metrics.delivered_today)}
        icon={<Mail className="size-5" />}
        tone="blue"
        trend="up"
      />
      {/* Card 2: Độ sâu hàng đợi hiện tại */}
      <MetricCard
        label="Queue Depth"
        value={formatCompactNumber(metrics.queued_now)}
        icon={<Activity className="size-5" />}
        tone="purple"
        trend="down"
      />
      {/* Card 3: Số lượng Consumers đang hoạt động / Tổng số */}
      <MetricCard
        label="Active Consumers"
        value={formatNumber(metrics.active_consumers)}
        sub={`${formatNumber(metrics.total_consumers)} total`}
        icon={<Users className="size-5" />}
        tone="green"
        trend="up"
      />
      {/* Card 4: Số lượng Gateways đang hoạt động / Tổng số */}
      <MetricCard
        label="Active Gateways"
        value={formatNumber(metrics.active_gateways)}
        sub={`${formatNumber(metrics.total_gateways)} total`}
        icon={<Server className="size-5" />}
        tone="blue"
        trend="up"
      />
      {/* Card 5: Tỷ lệ gửi thư thành công hiện tại */}
      <MetricCard
        label="Delivery Success Rate"
        value={`${metrics.delivery_success_rate.toFixed(2)}%`}
        icon={<Target className="size-5" />}
        tone="green"
        trend="up"
      />
    </div>
  )
}
