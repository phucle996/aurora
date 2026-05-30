import { useMemo, type ReactNode } from 'react'
import { ChevronDown } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

// Giá trị chart mặc định phòng trường hợp không có dữ liệu trả về từ API (24 giờ)
const fallbackChartData = Array.from({ length: 24 }, () => 0)

/**
 * Component Panel bọc các phần của biểu đồ và tiêu đề
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
 * Component hiển thị chú giải (legend) cho từng đường biểu đồ
 */
function Legend({ color, label, dashed }: { color: string; label: string; dashed?: boolean }) {
  return (
    <span className="inline-flex items-center gap-2">
      {/* Vẽ đường nét liền hoặc nét đứt tương thích với kiểu vẽ SVG */}
      <span className={cn('h-0.5 w-5 rounded-full', color, dashed && 'bg-transparent border-t-2 border-dashed border-current text-primary')} />
      {label}
    </span>
  )
}

/**
 * Hàm sinh chuỗi mô tả đường dẫn SVG d="..." dựa trên mảng giá trị đầu vào.
 * Tính toán tỷ lệ tọa độ X, Y tự động dựa trên giá trị lớn nhất và nhỏ nhất của mảng dữ liệu.
 */
function linePath(points: number[]) {
  const safePoints = points.length > 0 ? points : fallbackChartData
  const max = Math.max(...safePoints)
  const min = Math.min(...safePoints)
  const xStep = 575 / Math.max(safePoints.length - 1, 1)
  return safePoints
    .map((point, index) => {
      // Chiều rộng SVG tối đa là 575px bắt đầu từ offset 48px
      const x = 48 + index * xStep
      // Chiều cao tối đa vẽ biểu đồ là 120px và dịch chuyển từ trên xuống 150px
      const y = 150 - ((point - min) / Math.max(max - min, 1)) * 120
      return `${index === 0 ? 'M' : 'L'} ${x.toFixed(1)} ${y.toFixed(1)}`
    })
    .join(' ')
}

interface ThroughputChartProps {
  throughput: Array<{ label: string; delivered: number; queued: number; retries: number }>
}

/**
 * Component hiển thị biểu đồ thông lượng gửi thư dạng SVG tự vẽ cực kỳ mượt mà và trực quan
 * Giúp tối ưu hóa tốc độ tải và không phụ thuộc vào bất kỳ thư viện vẽ chart nặng nề nào.
 */
export function ThroughputChart({ throughput }: ThroughputChartProps) {
  // Lấy mảng dữ liệu riêng lẻ cho từng chuỗi số liệu nhờ useMemo để tránh re-calculate khi render lại
  const delivered = useMemo(() => throughput.map((point) => point.delivered), [throughput])
  const queued = useMemo(() => throughput.map((point) => point.queued), [throughput])
  const retries = useMemo(() => throughput.map((point) => point.retries), [throughput])
  const labels = useMemo(() => throughput.map((point) => point.label), [throughput])

  // Sinh đường dẫn SVG tương ứng cho 3 số liệu: Thành công, Đang xếp hàng và Số lượt thử lại
  const path = linePath(delivered)
  const path2 = linePath(queued)
  const path3 = linePath(retries)

  // Xác định nhãn trục thời gian hoành độ
  const axisLabels = labels.length > 0 ? labels : ['00:00', '06:00', '12:00', '18:00', '24:00']
  const labelAt = (index: number, fallback: string) => axisLabels[Math.min(index, axisLabels.length - 1)] ?? fallback

  return (
    <Panel
      title="Global Delivery Throughput (All Organizations)"
      action={
        <Button variant="outline" className="h-10 min-w-36 justify-between gap-3 border-border/80 bg-card px-4 aurora-filter-text shadow-sm cursor-pointer">
          <span className="flex items-center gap-2">24h</span>
          <ChevronDown className="size-4 text-muted-foreground" />
        </Button>
      }
    >
      <div className="h-57.5 w-full rounded-lg bg-linear-to-b from-primary/5 to-transparent px-2 pb-2 pt-1">
        {/* Phần chú thích màu sắc ở phía trên SVG */}
        <div className="mb-2 flex items-center gap-5 pl-8 aurora-table-cell">
          <Legend color="bg-primary" label="Delivered" />
          <Legend color="bg-primary/70" label="Queued" dashed />
          <Legend color="bg-emerald-500" label="Retries" dashed />
        </div>

        {/* Bản vẽ biểu đồ SVG tự tương thích */}
        <svg viewBox="0 0 640 180" className="h-45 w-full overflow-visible">
          {/* Vẽ các đường kẻ ngang hỗ trợ mắt nhìn tỷ lệ tốt hơn */}
          {[0, 45, 90, 135, 180].map((y) => (
            <line key={y} x1="48" x2="625" y1={y} y2={y} stroke="hsl(var(--border))" strokeOpacity="0.65" />
          ))}
          {/* Vẽ đường nét liền biểu diễn Delivered */}
          <path d={path} fill="none" stroke="hsl(var(--primary))" strokeWidth="3" strokeLinejoin="round" strokeLinecap="round" />
          {/* Vẽ đường đứt nét biểu diễn Queued */}
          <path d={path2} fill="none" stroke="hsl(var(--primary))" strokeDasharray="6 5" strokeWidth="2.5" strokeLinejoin="round" strokeLinecap="round" opacity=".8" />
          {/* Vẽ đường đứt nét biểu diễn Retries */}
          <path d={path3} fill="none" stroke="#10b981" strokeDasharray="6 5" strokeWidth="2.5" strokeLinejoin="round" strokeLinecap="round" />

          {/* Nhãn trục thời gian hoành độ phía dưới cùng */}
          <text x="48" y="176" className="fill-muted-foreground text-[12px]">{labelAt(0, '00:00')}</text>
          <text x="190" y="176" className="fill-muted-foreground text-[12px]">{labelAt(Math.floor(axisLabels.length * 0.25), '06:00')}</text>
          <text x="335" y="176" className="fill-muted-foreground text-[12px]">{labelAt(Math.floor(axisLabels.length * 0.5), '12:00')}</text>
          <text x="480" y="176" className="fill-muted-foreground text-[12px]">{labelAt(Math.floor(axisLabels.length * 0.75), '18:00')}</text>
          <text x="600" y="176" className="fill-muted-foreground text-[12px]">{labelAt(axisLabels.length - 1, '24:00')}</text>
        </svg>
      </div>
    </Panel>
  )
}
