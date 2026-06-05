import { useState } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { InputOTP, InputOTPGroup, InputOTPSlot } from '@/components/ui/input-otp'
import { Button } from '@/components/ui/button'
import { ShieldCheck, Loader2 } from 'lucide-react'

interface OTPVerificationDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: (otpCode: string) => Promise<void>
  title?: string
  description?: string
  confirmText?: string
  loading?: boolean
}

export function OTPVerificationDialog({
  open,
  onOpenChange,
  onConfirm,
  title = 'Security Verification',
  description = 'Please enter the 6-digit verification code from your authenticator app to authorize this action.',
  confirmText = 'Confirm',
  loading = false,
}: OTPVerificationDialogProps) {
  const [otpCode, setOtpCode] = useState('')

  const handleOpenChange = (newOpen: boolean) => {
    if (!newOpen) {
      setOtpCode('')
    }
    onOpenChange(newOpen)
  }

  const handleConfirm = async () => {
    if (otpCode.length < 6) return
    try {
      await onConfirm(otpCode)
      setOtpCode('') // Clear on success
    } catch {
      // Let parent handle any error states / display
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-110 border-[#dbe5f2] bg-white dark:border-slate-800 dark:bg-slate-950 p-0 overflow-hidden shadow-[0_20px_50px_rgba(8,112,184,0.15)] dark:shadow-[0_20px_50px_rgba(0,0,0,0.5)]">
        <div className="relative p-6 pt-8 pb-4 text-center">
          <div className="absolute inset-0 bg-linear-to-b from-primary/5 via-transparent to-transparent pointer-events-none" />
          <div className="mx-auto mb-5 flex h-14 w-14 items-center justify-center rounded-full bg-primary/10 text-primary dark:bg-blue-500/10 dark:text-blue-400 ring-8 ring-primary/5 dark:ring-blue-500/5 animate-pulse">
            <ShieldCheck className="h-7 w-7" />
          </div>
          <DialogHeader className="space-y-2">
            <DialogTitle className="text-xl font-bold tracking-tight text-slate-900 dark:text-slate-50 text-center">
              {title}
            </DialogTitle>
            <DialogDescription className="text-sm text-slate-500 dark:text-slate-400 px-2 leading-relaxed text-center">
              {description}
            </DialogDescription>
          </DialogHeader>
        </div>

        <div className="px-6 py-4 flex flex-col items-center justify-center bg-slate-50/50 dark:bg-slate-900/30 border-y border-slate-100 dark:border-slate-800/60">
          <div className="my-2">
            <InputOTP
              maxLength={6}
              value={otpCode}
              onChange={(val) => setOtpCode(val.replace(/\D/g, ''))}
              disabled={loading}
              autoFocus
            >
              <InputOTPGroup className="dark:text-slate-100 gap-1.5">
                <InputOTPSlot index={0} className="h-12 w-11 rounded-lg border bg-white text-lg font-bold shadow-sm transition-all focus-visible:ring-2 focus-visible:ring-primary dark:bg-slate-950 dark:border-slate-800 dark:focus-visible:ring-blue-500" />
                <InputOTPSlot index={1} className="h-12 w-11 rounded-lg border bg-white text-lg font-bold shadow-sm transition-all focus-visible:ring-2 focus-visible:ring-primary dark:bg-slate-950 dark:border-slate-800 dark:focus-visible:ring-blue-500" />
                <InputOTPSlot index={2} className="h-12 w-11 rounded-lg border bg-white text-lg font-bold shadow-sm transition-all focus-visible:ring-2 focus-visible:ring-primary dark:bg-slate-950 dark:border-slate-800 dark:focus-visible:ring-blue-500" />
                <InputOTPSlot index={3} className="h-12 w-11 rounded-lg border bg-white text-lg font-bold shadow-sm transition-all focus-visible:ring-2 focus-visible:ring-primary dark:bg-slate-950 dark:border-slate-800 dark:focus-visible:ring-blue-500" />
                <InputOTPSlot index={4} className="h-12 w-11 rounded-lg border bg-white text-lg font-bold shadow-sm transition-all focus-visible:ring-2 focus-visible:ring-primary dark:bg-slate-950 dark:border-slate-800 dark:focus-visible:ring-blue-500" />
                <InputOTPSlot index={5} className="h-12 w-11 rounded-lg border bg-white text-lg font-bold shadow-sm transition-all focus-visible:ring-2 focus-visible:ring-primary dark:bg-slate-950 dark:border-slate-800 dark:focus-visible:ring-blue-500" />
              </InputOTPGroup>
            </InputOTP>
          </div>
        </div>

        <DialogFooter className="p-6 gap-3 sm:gap-3 bg-white dark:bg-slate-950">
          <Button
            variant="outline"
            onClick={() => handleOpenChange(false)}
            disabled={loading}
            className="h-11 rounded-lg px-5 text-sm font-semibold border-slate-200 dark:border-slate-800 dark:text-slate-300"
          >
            Cancel
          </Button>
          <Button
            onClick={handleConfirm}
            disabled={loading || otpCode.length < 6}
            className="h-11 rounded-lg px-6 text-sm font-semibold shadow-sm"
          >
            {loading ? (
              <>
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                Verifying...
              </>
            ) : (
              confirmText
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
