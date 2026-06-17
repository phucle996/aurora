import {
  CheckCircle2,
  Loader2,
  XCircle,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface TestConnectionDialogProps {
  isOpen: boolean                         // Trạng thái mở/đóng của dialog
  onOpenChange: (open: boolean) => void   // Hàm thay đổi trạng thái ẩn hiện dialog
  loading: boolean                        // Trạng thái đang chạy tiến trình test
  success: boolean | null                 // Trạng thái kết quả test thành công/thất bại
  message: string                         // Nội dung chi tiết phản hồi chẩn đoán từ máy chủ SMTP
  endpointName: string                    // Tên SMTP Endpoint đang thực hiện kiểm tra
}

/**
 * Cửa sổ hội thoại nổi chẩn đoán khả năng kết nối SMTP trực tiếp (TestConnectionDialog).
 * Được thiết kế lại theo phong cách tối giản phẳng cao cấp (Flat Premium Style)
 * đồng bộ hoàn hảo với hệ thống thiết kế chung của ứng dụng.
 */
export function TestConnectionDialog({
  isOpen,
  onOpenChange,
  loading,
  success,
  message,
  endpointName,
}: TestConnectionDialogProps) {
  return (
    <Dialog open={isOpen} onOpenChange={onOpenChange}>
      <DialogContent showCloseButton={!loading} className="sm:max-w-md p-6">
        <DialogHeader className="flex flex-col items-center text-center space-y-4 pt-4">
          {/* Icon trạng thái tròn phẳng, kích thước tiêu chuẩn, tự động đổi màu */}
          <div className={cn(
            "flex size-14 items-center justify-center rounded-full transition-all duration-300",
            loading ? "bg-primary/10 text-primary animate-pulse" : success ? "bg-emerald-50 text-emerald-600 dark:bg-emerald-500/10 dark:text-emerald-400" : "bg-rose-50 text-rose-600 dark:bg-rose-500/10 dark:text-rose-400"
          )}>
            {loading ? (
              <Loader2 className="size-6 animate-spin" />
            ) : success ? (
              <CheckCircle2 className="size-6" />
            ) : (
              <XCircle className="size-6" />
            )}
          </div>

          <div className="space-y-1.5 w-full">
            {/* Tiêu đề trạng thái kết quả của phiên kiểm tra */}
            <DialogTitle className="text-xl font-bold tracking-tight text-foreground">
              {loading ? 'Testing Connection' : success ? 'Connection Successful' : 'Connection Failed'}
            </DialogTitle>
            
            {/* Hiển thị tên endpoint dạng monospace nhỏ gọn */}
            <p className="text-[11px] font-mono font-semibold uppercase tracking-wider text-muted-foreground">
              {endpointName || 'SMTP Endpoint'}
            </p>
          </div>

          {/* Vùng log chẩn đoán: Box tối giản dạng font monospace, dễ dàng xem logs */}
          <DialogDescription className="text-sm text-muted-foreground w-full bg-muted/40 dark:bg-muted/10 rounded-lg p-4 font-mono text-left max-h-48 overflow-y-auto border border-border/40 leading-relaxed break-words">
            {loading ? (
              <span className="flex items-center gap-2 text-foreground/80">
                <Loader2 className="size-3.5 animate-spin shrink-0 text-primary" />
                {message || 'Establishing TCP handshake and negotiating TLS connection...'}
              </span>
            ) : (
              message || 'No diagnostic output returned.'
            )}
          </DialogDescription>
        </DialogHeader>

        {/* Cụm nút bấm chân trang (Footer Actions) */}
        <DialogFooter className="sm:justify-stretch mt-4 w-full">
          <Button
            type="button"
            onClick={() => onOpenChange(false)}
            variant={loading ? "secondary" : success ? "default" : "destructive"}
            className={cn(
              "h-12 w-full rounded-lg font-semibold transition-all cursor-pointer",
              success && "bg-emerald-600 hover:bg-emerald-700 text-white dark:bg-emerald-500 dark:hover:bg-emerald-600 border-none"
            )}
            disabled={loading}
          >
            {loading ? 'Testing...' : 'Close'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
