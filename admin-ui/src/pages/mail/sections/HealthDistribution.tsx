import { useMemo, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

// Hàm định dạng số hiển thị theo định dạng tiếng Anh Mỹ (ví dụ: 1,234)
function formatNumber(value: number) {
  return new Intl.NumberFormat('en-US').format(value)
}

/**
 * Component Panel bọc nội dung chỉ số và tiêu đề
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
 * Component Donut hiển thị tỷ lệ phân bố sức khỏe của tài nguyên dưới dạng biểu đồ hình khuyên (conic-gradient)
 * Đi kèm bảng thống kê chi tiết bên phải giúp việc theo dõi trực quan hơn.
 */
function Donut({ total, segments, centerLabel = 'Total' }: { total: string; segments: Array<{ label: string; value: string; color: string }>; centerLabel?: string }) {
  // Tính toán góc phân bố (conic-gradient) từ danh sách các phần (segments) để làm hình khuyên tròn
  const gradient = useMemo(() => {
    const step = 100 / segments.length
    return segments.map((s, i) => `${s.color} ${i * step}% ${(i + 1) * step}%`).join(', ')
  }, [segments])

  return (
    <div className="flex flex-col items-center justify-center gap-6 sm:flex-row sm:gap-8 h-full min-h-57.5">
      {/* Vòng khuyên biểu đồ sử dụng conic-gradient CSS và một hình tròn phủ giữa làm rỗng */}
      <div className="relative size-36 rounded-full shrink-0" style={{ background: `conic-gradient(${gradient})` }}>
        <div className="absolute inset-7 flex flex-col items-center justify-center rounded-full bg-card shadow-inner">
          <span className="aurora-metric-value">{total}</span>
          <span className="text-xs font-medium text-muted-foreground">{centerLabel}</span>
        </div>
      </div>

      {/* Danh sách các chú thích và thông số phần trăm chi tiết của từng phân loại */}
      <div className="space-y-2 text-sm w-full max-w-50">
        {segments.map((segment) => (
          <div key={segment.label} className="flex items-center justify-between gap-4">
            <span className="inline-flex items-center gap-2 font-medium text-muted-foreground">
              {/* Dấu chấm màu đại diện tương ứng */}
              <span className="size-2 rounded-full" style={{ background: segment.color }} />
              {segment.label}
            </span>
            <span className="font-semibold text-foreground">{segment.value}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

interface HealthDistributionProps {
  // Dữ liệu sức khỏe bao gồm các đếm số lượng: lành mạnh, cảnh báo, dừng hoạt động và không xác định
  health: { healthy: number; warning: number; stopped: number; unknown: number }
}

/**
 * Component hiển thị sự phân bổ trạng thái sức khỏe của các Endpoints
 * Giúp người quản trị (SRE) nhanh chóng phát hiện các phần tử bị lỗi hoặc suy giảm hiệu năng.
 */
export function HealthDistribution({ health }: HealthDistributionProps) {
  // Tính tổng số lượng endpoints đang được giám sát
  const totalComponents = health.healthy + health.warning + health.stopped + health.unknown

  // Hàm nội bộ giúp định dạng nhanh từng segment kèm tính toán tỷ lệ phần trăm tương ứng
  const formatSegmentValue = (value: number, total: number) => {
    const percent = total > 0 ? (value / total) * 100 : 0
    return `${formatNumber(value)} (${percent.toFixed(1)}%)`
  }

  // Danh sách các nhóm trạng thái sức khỏe với mã màu tiêu chuẩn (Xanh lục, Vàng, Đỏ, Xám)
  const healthSegments = useMemo(() => [
    { label: 'Healthy', value: formatSegmentValue(health.healthy, totalComponents), color: '#22c55e' },
    { label: 'Degraded', value: formatSegmentValue(health.warning, totalComponents), color: '#f59e0b' },
    { label: 'Unhealthy', value: formatSegmentValue(health.stopped, totalComponents), color: '#ef4444' },
    { label: 'Unknown', value: formatSegmentValue(health.unknown, totalComponents), color: '#94a3b8' },
  ], [health, totalComponents])

  return (
    <Panel title="Endpoint Health Distribution">
      <Donut total={formatNumber(totalComponents)} centerLabel="Endpoints" segments={healthSegments} />
    </Panel>
  )
}
