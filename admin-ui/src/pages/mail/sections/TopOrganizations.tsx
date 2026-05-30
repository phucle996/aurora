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

// Hàm định dạng số dạng rút gọn (ví dụ: 1.2K, 3.4M) giúp tối ưu diện tích hiển thị trên các màn hình nhỏ
function formatCompactNumber(value: number) {
  return new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 2 }).format(value)
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

type TopOrg = {
  tenant_id: string       // Mã định danh của tổ chức/khách hàng (tenant)
  delivered: number       // Số lượng thư đã gửi thành công
  total_attempts: number  // Tổng số lần thử gửi thư
  success_rate: number    // Tỷ lệ gửi thành công (%)
  queued: number          // Số lượng thư hiện đang xếp hàng
}

interface TopOrganizationsProps {
  organizations: TopOrg[]
}

/**
 * Component hiển thị danh sách xếp hạng các tổ chức theo lưu lượng gửi thư lớn nhất
 * Giúp người quản trị biết khách hàng nào đang sử dụng tài nguyên hệ thống nhiều nhất.
 */
export function TopOrganizations({ organizations }: TopOrganizationsProps) {
  return (
    <Panel
      title="Top Organizations by Delivery Volume (24h)"
      action={<a className="aurora-link-text hover:underline cursor-pointer">View all organizations</a>}
    >
      {organizations.length === 0 ? (
        /* Hiển thị trạng thái trống nếu chưa có lượt truyền nhận dữ liệu nào từ các tổ chức */
        <EmptyState
          title="No organization delivery yet"
          description="Records without tenant_id are valid, but they are not grouped into organizations."
        />
      ) : (
        /* Hiển thị bảng xếp hạng chi tiết */
        <CompactTable
          rows={organizations.map((row, index) => [
            String(index + 1), // Thứ hạng
            row.tenant_id,     // Mã tổ chức
            formatCompactNumber(row.delivered), // Thư đã gửi
            `${row.success_rate.toFixed(2)}%`, // Tỷ lệ thành công
            formatCompactNumber(row.queued), // Thư đang xếp hàng
          ])}
        />
      )}
    </Panel>
  )
}
