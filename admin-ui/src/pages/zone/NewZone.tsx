import { useState } from 'react'
import { Link, useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'
import { ShieldCheck, Loader2 } from 'lucide-react'

import { Fetch } from '@/lib/fetch'
import { PageContent } from '@/components/layout/layout'
import { Button } from '@/components/ui/button'
import { type ZoneLocation } from '@/components/zone/location-autocomplete'
import { useAdminSession } from '@/hooks/useAdminSession'
import { getOrCreateDeviceKeys, generateNonce, sha256Hex, signPayload } from '@/lib/crypto'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import {
  InputOTP,
  InputOTPGroup,
  InputOTPSlot,
} from '@/components/ui/input-otp'

import ZoneForm, { type ServiceKey } from './sections/ZoneForm'
import ZonePreviewCard from './sections/ZonePreviewCard'

function slugifyZoneCode(value: string) {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

async function readAPIMessage(response: Response) {
  try {
    const payload = (await response.json()) as { message?: string; error?: string }
    return payload.message || payload.error || 'Request failed'
  } catch {
    return 'Request failed'
  }
}

export default function NewZonePage() {
  const router = useRouter()
  const [zoneName, setZoneName] = useState('')
  const [zoneCode, setZoneCode] = useState('')
  const [isZoneCodeManuallyEdited, setIsZoneCodeManuallyEdited] = useState(false)
  const [location, setLocation] = useState('')
  const [description, setDescription] = useState('')

  const { session } = useAdminSession()
  const [services, setServices] = useState<Record<ServiceKey, boolean>>({
    hypervisor: true,
    storage: true,
    mail: false,
    k8s: true,
    ai: false,
  })
  const [isOTPOpen, setIsOTPOpen] = useState(false)
  const [otpCode, setOtpCode] = useState('')
  const [signing, setSigning] = useState(false)

  const toggleService = (key: ServiceKey) => {
    setServices((current) => ({ ...current, [key]: !current[key] }))
  }

  const selectLocation = (item: ZoneLocation) => {
    setLocation(item.label)
    if (!zoneCode.trim()) {
      setZoneCode(item.suggestedCode)
    }
  }

  const trimmedName = zoneName.trim()
  const trimmedCode = zoneCode.trim()
  const trimmedLocation = location.trim()
  const canSubmit = trimmedName !== '' && trimmedCode !== '' && trimmedLocation !== '' && !signing

  const handleTriggerOTP = () => {
    if (!canSubmit) {
      toast.error('Please fill in zone name, code, and location before creating the zone.')
      return
    }
    setOtpCode('')
    setIsOTPOpen(true)
  }

  const confirmCreateZoneWithOTP = async () => {
    if (otpCode.length < 6) {
      toast.error('Please enter a valid 6-digit verification code.')
      return
    }

    setSigning(true)
    try {
      if (!session?.accessKey) {
        throw new Error('Admin session key not found. Please log out and sign in again.')
      }

      const deviceKeys = await getOrCreateDeviceKeys()
      if (!deviceKeys.privateKey) {
        throw new Error('Security keys are missing on this device. Please log out and sign in again to register your keys.')
      }

      const bodyString = JSON.stringify({
        name: trimmedName,
        code: slugifyZoneCode(trimmedCode),
        location: trimmedLocation,
        description: description.trim(),
        enable_hypervisor: services.hypervisor,
        enable_storage: services.storage,
        enable_mail: services.mail,
        enable_k8s: services.k8s,
        enable_ai: services.ai,
      })

      const bodyHash = await sha256Hex(bodyString)
      const timestamp = Math.floor(Date.now() / 1000).toString()
      const nonce = generateNonce()
      
      const payloadStr = `POST\n/admin/core/zones\n\n${bodyHash}\n${timestamp}\n${nonce}\n${session.accessKey}`
      const signature = await signPayload(payloadStr, deviceKeys.privateKey)

      const response = await Fetch('/admin/core/zones', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Admin-Signature': signature,
          'X-Admin-Timestamp': timestamp,
          'X-Admin-Nonce': nonce,
          'X-Admin-StepUp-Code': otpCode,
        },
        body: bodyString,
      })

      if (!response.ok) {
        throw new Error(await readAPIMessage(response))
      }

      toast.success('Zone created successfully!')
      setIsOTPOpen(false)
      router.navigate({ to: '/zones' })
    } catch (err) {
      const errMsg = err instanceof Error ? err.message : 'Cannot create zone'
      toast.error(errMsg)
    } finally {
      setSigning(false)
    }
  }

  return (
    <PageContent className="pb-0">
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-4">
          <nav className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
            <Link to="/zones" className="text-primary hover:underline">
              Zone
            </Link>
            <span>/</span>
            <span>Add Zone</span>
          </nav>
          <div className="space-y-2">
            <h1 className="text-3xl font-semibold tracking-[-0.03em] text-foreground md:text-4xl">
              Add Zone
            </h1>
            <p className="text-sm text-muted-foreground md:text-base">
              Create a new infrastructure zone for the platform.
            </p>
          </div>
        </div>

        <div className="flex items-center gap-3 lg:pt-10">
          <Button asChild variant="outline" className="h-12 rounded-lg px-8 text-sm font-semibold">
            <Link to="/zones">Cancel</Link>
          </Button>
          <Button className="h-12 rounded-lg px-8 text-sm font-semibold shadow-sm" onClick={handleTriggerOTP} disabled={!canSubmit}>
            {signing ? 'Creating...' : 'Create Zone'}
          </Button>
        </div>
      </div>

      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_400px]">
        <ZoneForm
          zoneName={zoneName}
          setZoneName={setZoneName}
          zoneCode={zoneCode}
          setZoneCode={setZoneCode}
          isZoneCodeManuallyEdited={isZoneCodeManuallyEdited}
          setIsZoneCodeManuallyEdited={setIsZoneCodeManuallyEdited}
          location={location}
          setLocation={setLocation}
          description={description}
          setDescription={setDescription}
          services={services}
          toggleService={toggleService}
          selectLocation={selectLocation}
        />
        <ZonePreviewCard
          zoneName={zoneName}
          location={location}
          description={description}
        />
      </div>

      <Dialog open={isOTPOpen} onOpenChange={setIsOTPOpen}>
        <DialogContent className="sm:max-w-110 border-[#dbe5f2] bg-white dark:border-slate-800 dark:bg-slate-950 p-0 overflow-hidden shadow-[0_20px_50px_rgba(8,112,184,0.15)] dark:shadow-[0_20px_50px_rgba(0,0,0,0.5)]">
          <div className="relative p-6 pt-8 pb-4 text-center">
            <div className="absolute inset-0 bg-linear-to-b from-primary/5 via-transparent to-transparent pointer-events-none" />
            
            <div className="mx-auto mb-5 flex h-14 w-14 items-center justify-center rounded-full bg-primary/10 text-primary dark:bg-blue-500/10 dark:text-blue-400 ring-8 ring-primary/5 dark:ring-blue-500/5 animate-pulse">
              <ShieldCheck className="h-7 w-7" />
            </div>

            <DialogHeader className="space-y-2">
              <DialogTitle className="text-xl font-bold tracking-tight text-slate-900 dark:text-slate-50">
                Security Verification
              </DialogTitle>
              <DialogDescription className="text-sm text-slate-500 dark:text-slate-400 px-2 leading-relaxed">
                Zone creation is a critical operation. Please enter the 6-digit verification code from your authenticator app to authorize this action.
              </DialogDescription>
            </DialogHeader>
          </div>

          <div className="px-6 py-4 flex flex-col items-center justify-center bg-slate-50/50 dark:bg-slate-900/30 border-y border-slate-100 dark:border-slate-800/60">
            <div className="my-2">
              <InputOTP
                maxLength={6}
                value={otpCode}
                onChange={(val) => setOtpCode(val.replace(/\D/g, ''))}
                disabled={signing}
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
              onClick={() => setIsOTPOpen(false)}
              disabled={signing}
              className="h-11 rounded-lg px-5 text-sm font-semibold border-slate-200 dark:border-slate-800 dark:text-slate-300"
            >
              Cancel
            </Button>
            <Button
              onClick={confirmCreateZoneWithOTP}
              disabled={signing || otpCode.length < 6}
              className="h-11 rounded-lg px-6 text-sm font-semibold shadow-sm"
            >
              {signing ? (
                <>
                  <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                  Verifying...
                </>
              ) : (
                'Create Zone'
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </PageContent>
  )
}
