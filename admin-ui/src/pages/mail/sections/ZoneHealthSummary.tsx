import { type ReactNode } from 'react'
import { cn } from '@/lib/utils'
import { Table, TableBody, TableCell, TableRow } from '@/components/ui/table'

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

// Định dạng số nguyên sang chuẩn chuỗi định dạng quốc tế (ví dụ: 1,000)
function formatNumber(value: number) {
  return new Intl.NumberFormat('en-US').format(value)
}

// Xác định màu sắc hiển thị phù hợp với từng trạng thái/từ khóa sức khỏe
function statusClass(value: string) {
  return value === 'Healthy' || value === 'Active'
    ? 'text-emerald-600 dark:text-emerald-400 font-semibold'
    : value === 'Degraded' || value === 'Medium' || value === 'Suspended'
      ? 'text-amber-600 dark:text-amber-400'
      : value === 'High' || value === 'Unhealthy'
        ? 'text-destructive font-semibold'
        : ''
}

/**
 * Component bảng nhỏ gọn hiển thị dữ liệu dạng ma trận hàng và cột đơn giản
 */
function CompactTable({ rows }: { rows: string[][] }) {
  return (
    <Table>
      <TableBody>
        {rows.map((row) => (
          <TableRow key={row.join('-')} className="h-9">
            {row.map((cell, index) => (
              <TableCell
                key={index}
                className={cn(
                  'aurora-table-cell',
                  index === 0 && 'aurora-table-key',
                  index === row.length - 1 && statusClass(cell)
                )}
              >
                {cell}
              </TableCell>
            ))}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

type ZoneHealth = {
  zone_id: string     // Mã định danh vùng dữ liệu hạ tầng
  healthy: number     // Số lượng tài nguyên đang hoạt động tốt
  degraded: number    // Số lượng tài nguyên bị suy giảm hiệu suất
  unhealthy: number   // Số lượng tài nguyên bị lỗi/ngắt hoạt động
  total: number       // Tổng số lượng tài nguyên trong vùng
  status: string      // Trạng thái tổng quát của vùng hạ tầng
}

interface ZoneHealthSummaryProps {
  zones: ZoneHealth[]
}

/**
 * Component hiển thị thông tin sức khỏe hạ tầng chia theo từng Vùng (Zone) địa lý/cluster.
 * Cực kỳ quan trọng để đội ngũ vận hành (SRE) phát hiện sự cố mang tính cục bộ của các data center.
 */
export function ZoneHealthSummary({ zones }: ZoneHealthSummaryProps) {
  // Chuẩn bị dữ liệu hiển thị dạng dòng bảng
  const rows = zones.length === 0
    ? [['No zones yet', '0', '0', '0', 'Healthy']]
    : zones.map((row) => [
        row.zone_id,                    // Tên hoặc mã zone hạ tầng
        formatNumber(row.healthy),      // Số tài nguyên Healthy
        formatNumber(row.degraded),     // Số tài nguyên Degraded
        formatNumber(row.unhealthy),    // Số tài nguyên Unhealthy
        row.status,                     // Trạng thái chung
      ])

  return (
    <Panel title="Zone Health Summary">
      <CompactTable rows={rows} />
    </Panel>
  )
}
