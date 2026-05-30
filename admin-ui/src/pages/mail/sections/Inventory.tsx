import { useState, useMemo, type ReactNode } from 'react'
import { Search } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

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
 * Component hiển thị nhãn trạng thái (Status Badge) được phối màu sắc trực quan theo nhóm
 */
function StatusBadge({ value }: { value: string }) {
  const isGood = value === 'Healthy' || value === 'Active' || value === 'Enabled' || value === 'delivered'
  const isWarn = value === 'Degraded' || value === 'Suspended' || value === 'dead_letter'
  return (
    <Badge
      variant="secondary"
      className={cn(
        'aurora-caption font-semibold px-2 py-0.5',
        isGood
          ? 'bg-emerald-500/10 text-emerald-700 dark:bg-emerald-500/20 dark:text-emerald-400'
          : isWarn
            ? 'bg-amber-500/10 text-amber-700 dark:bg-amber-500/20 dark:text-amber-400'
            : 'bg-slate-500/10 text-slate-700 dark:bg-slate-500/20 dark:text-slate-400'
      )}
    >
      {value}
    </Badge>
  )
}

interface InventoryProps {
  title: string       // Tiêu đề của bảng danh sách tài nguyên
  columns: string[]   // Danh sách tên các cột tiêu đề
  rows: string[][]    // Ma trận dữ liệu hiển thị (mỗi hàng là một mảng chuỗi)
}

/**
 * Component bảng danh sách tài nguyên linh động (Fleet Inventory)
 * Tích hợp sẵn thanh tìm kiếm nhanh (Search Input) lọc dữ liệu trực tiếp ở client-side rất mượt mà.
 */
export function Inventory({ title, columns, rows }: InventoryProps) {
  // State lưu trữ từ khóa tìm kiếm của người dùng
  const [query, setQuery] = useState('')

  // Lọc dữ liệu hàng trực tiếp trên client nhờ useMemo giúp phản hồi tức thì khi người dùng gõ từ khóa
  const filteredRows = useMemo(() => {
    if (!query.trim()) return rows
    const term = query.toLowerCase()
    return rows.filter((row) =>
      row.some((cell) => cell.toLowerCase().includes(term))
    )
  }, [rows, query])

  return (
    <Panel
      title={title}
      action={
        /* Thanh tìm kiếm nhanh tích hợp icon */
        <div className="relative w-48 sm:w-60">
          <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="h-9 pl-9 rounded-lg border-border/80 bg-card shadow-sm text-sm"
            placeholder="Search fleet..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
      }
    >
      {/* Container của bảng dữ liệu có thanh cuộn ngang tự động tương thích với mobile */}
      <div className="rounded-xl border border-border/60 bg-muted/10 overflow-x-auto">
        <Table>
          <TableHeader>
            <TableRow className="bg-muted/40 hover:bg-muted/40">
              {columns.map((column) => (
                <TableHead key={column} className="aurora-table-head py-3 font-semibold text-foreground/80">
                  {column}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {filteredRows.length === 0 ? (
              /* Trạng thái khi không tìm thấy tài nguyên nào phù hợp */
              <TableRow>
                <TableCell colSpan={columns.length} className="h-32 text-center text-muted-foreground font-medium">
                  No matching resources found
                </TableCell>
              </TableRow>
            ) : (
              /* Danh sách các dòng dữ liệu */
              filteredRows.map((row, index) => (
                <TableRow key={row[0] || index} className="h-10 hover:bg-muted/10 transition-colors">
                  {row.map((cell, idx) => (
                    <TableCell key={idx} className="aurora-table-cell py-2.5">
                      {/* Tự động chuyển cột sát cuối cùng (thường là cột Status) thành StatusBadge */}
                      {idx === row.length - 2 ? (
                        <StatusBadge value={cell} />
                      ) : (
                        cell
                      )}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>
    </Panel>
  )
}
