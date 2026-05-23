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
  isOpen: boolean
  onOpenChange: (open: boolean) => void
  loading: boolean
  success: boolean | null
  message: string
  endpointName: string
}

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
      <DialogContent showCloseButton={false} className="sm:max-w-[440px] overflow-hidden border-none p-0 bg-transparent shadow-none ring-0">
        <div className="relative overflow-hidden rounded-3xl border border-white/10 bg-card p-8 shadow-2xl backdrop-blur-xl ring-1 ring-white/10">
          {/* Background decorative glow */}
          <div className={cn(
            "absolute -right-20 -top-20 h-40 w-40 rounded-full blur-[80px] transition-colors duration-500",
            loading ? "bg-primary/20" : success ? "bg-emerald-500/20" : "bg-rose-500/20"
          )} />
          
          <DialogHeader className="relative flex flex-col items-center text-center">
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
            <DialogTitle className="text-2xl font-bold tracking-tight text-foreground">
              {loading ? 'Testing Connection' : success ? 'Connection Successful' : 'Connection Failed'}
            </DialogTitle>
            <DialogDescription className="mt-2 text-base text-muted-foreground">
              {loading ? (
                <>Establishing connection to <span className="font-medium text-foreground">{endpointName}</span>...</>
              ) : (
                message
              )}
            </DialogDescription>
          </DialogHeader>

          <DialogFooter className="relative mt-8 sm:justify-center">
            <Button 
              type="button" 
              onClick={() => onOpenChange(false)}
              className={cn(
                "h-11 w-full rounded-xl font-semibold shadow-lg transition-all active:scale-95",
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
