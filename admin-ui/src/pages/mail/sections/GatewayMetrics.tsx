import { ArrowDown, ArrowUp, Server, ShieldCheck, Clock3, Link2, Target } from 'lucide-react'
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
 * Component hiển thị một thẻ chỉ số đơn lẻ cho Gateways
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

interface GatewayMetricsProps {
  metrics: {
    total_gateways: number     // Tổng số Gateways cấu hình trong hệ thống
    active_gateways: number    // Số Gateways đang hoạt động
    disabled_gateways: number  // Số Gateways tạm ngắt hoặc disable
    ready_shards: number       // Số lượng phân mảnh (Shards) đã sẵn sàng hoạt động
    pending_shards: number     // Số lượng phân mảnh đang chờ kích hoạt
    draining_shards: number    // Số lượng phân mảnh đang trong tiến trình rút cạn kết nối (draining)
    bound_endpoints: number    // Số lượng Endpoints đã được liên kết trong định tuyến
  }
}

/**
 * Component lưới hiển thị các thẻ metrics đo lường sức khỏe và trạng thái của hạ tầng Gateway.
 * Giúp đội ngũ vận hành biết số lượng shards đang chạy hoặc phân mảnh bị treo (pending/draining shards).
 */
export function GatewayMetrics({ metrics }: GatewayMetricsProps) {
  return (
    <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-6">
      {/* 1. Tổng số Gateways */}
      <MetricCard
        label="Total Gateways"
        value={formatNumber(metrics.total_gateways)}
        trend="up"
        icon={<Server className="size-5" />}
        tone="purple"
      />
      {/* 2. Số lượng Gateway đang hoạt động và số lượng bị dừng */}
      <MetricCard
        label="Healthy Gateways"
        value={formatNumber(metrics.active_gateways)}
        sub={`${formatNumber(metrics.disabled_gateways)} disabled`}
        trend="up"
        icon={<ShieldCheck className="size-5" />}
        tone="green"
      />
      {/* 3. Số phân mảnh Shards đã sẵn sàng hoạt động */}
      <MetricCard
        label="Ready Shards"
        value={formatNumber(metrics.ready_shards)}
        trend="up"
        icon={<Server className="size-5" />}
        tone="blue"
      />
      {/* 4. Tổng phân mảnh Shards đang treo hoặc chờ rút cạn kết nối */}
      <MetricCard
        label="Pending + Draining Shards"
        value={formatNumber(metrics.pending_shards + metrics.draining_shards)}
        trend="down"
        icon={<Clock3 className="size-5" />}
        tone="amber"
      />
      {/* 5. Số lượng Endpoints đang hoạt động trong định tuyến toàn cục */}
      <MetricCard
        label="Active Endpoint Pool"
        value={formatNumber(metrics.bound_endpoints)}
        sub="global routing pool"
        trend="up"
        icon={<Link2 className="size-5" />}
        tone="blue"
      />
      {/* 6. Độ trễ gửi thư trung bình (không được theo dõi trực tiếp trong DB hiện tại) */}
      <MetricCard
        label="Avg Delivery Latency"
        value="N/A"
        sub="not tracked in DB"
        trend="down"
        icon={<Target className="size-5" />}
        tone="slate"
      />
    </div>
  )
}
