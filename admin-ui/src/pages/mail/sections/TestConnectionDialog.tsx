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
  success: boolean | null                 // Kết quả test thành công hay thất bại (null nếu chưa chạy)
  message: string                         // Nội dung chi tiết phản hồi từ máy chủ SMTP
  endpointName: string                    // Tên Endpoint đang thực hiện kiểm tra
}

/**
 * Cửa sổ hội thoại nổi chẩn đoán khả năng kết nối SMTP trực tiếp (TestConnectionDialog)
 * Thiết kế giao diện kính mờ cao cấp (Glassmorphism) với hiệu ứng vầng sáng đổi màu thích ứng theo ba trạng thái (Đang chờ - Xanh lục - Đỏ hồng).
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
      <DialogContent showCloseButton={false} className="sm:max-w-110 overflow-hidden border-none p-0 bg-transparent shadow-none ring-0">
        <div className="relative overflow-hidden rounded-3xl border border-white/10 bg-card p-8 shadow-2xl backdrop-blur-xl ring-1 ring-white/10">
          {/* Vầng sáng trang trí nền tự động thay đổi màu sắc thích ứng */}
          <div className={cn(
            "absolute -right-20 -top-20 h-40 w-40 rounded-full blur-[80px] transition-colors duration-500",
            loading ? "bg-primary/20" : success ? "bg-emerald-500/20" : "bg-rose-500/20"
          )} />

          <DialogHeader className="relative flex flex-col items-center text-center">
            {/* Vùng vẽ Icon thích ứng màu nền */}
            <div className={cn(
              "mb-6 flex size-16 items-center justify-center rounded-2xl shadow-inner transition-colors duration-500",
              loading ? "bg-primary/10 text-primary" : success ? "bg-emerald-500/10 text-emerald-500" : "bg-rose-500/10 text-rose-500"
            )}>
              {loading ? (
                <Loader2 className="size-8 animate-spin" />
              ) : success ? (
                <CheckCircle2 className="size-8" />
              ) : (
                <XCircle className="size-8" />
              )}
            </div>
            {/* Tiêu đề trạng thái kết quả */}
            <DialogTitle className="text-2xl font-bold tracking-tight text-foreground">
              {loading ? 'Testing Connection' : success ? 'Connection Successful' : 'Connection Failed'}
            </DialogTitle>
            {/* Nội dung thông điệp chi tiết */}
            <DialogDescription className="mt-2 text-base text-muted-foreground">
              {loading ? (
                <>Establishing connection to <span className="font-medium text-foreground">{endpointName}</span>...</>
              ) : (
                message
              )}
            </DialogDescription>
          </DialogHeader>

          {/* Hàng nút dưới cùng để đóng dialog nhanh */}
          <DialogFooter className="relative mt-8 sm:justify-center">
            <Button
              type="button"
              onClick={() => onOpenChange(false)}
              className={cn(
                "h-11 w-full rounded-xl font-semibold shadow-lg transition-all active:scale-95 cursor-pointer",
                loading ? "bg-muted text-muted-foreground" : success
                  ? "bg-emerald-500 hover:bg-emerald-600 text-white shadow-emerald-500/20"
                  : "bg-rose-500 hover:bg-rose-600 text-white shadow-rose-500/20"
              )}
              disabled={loading}
            >
              {loading ? 'Please wait...' : 'Close'}
            </Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>
  )
}
