import { useState } from 'react'
import { Server, Database, Layers3, Cpu, PackageCheck, Clock, AlertTriangle } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Switch } from '@/components/ui/switch'
import { Fetch } from '@/lib/fetch'
import { getOrCreateDeviceKeys, generateNonce, sha256Hex, signPayload } from '@/lib/crypto'
import { OTPVerificationDialog } from '@/components/zone/OTPVerificationDialog'
import { type ZoneServiceHealth } from './ZoneServicesPanel'

const fullServiceCatalog = [
  { key: 'hypervisor', label: 'Hypervisor', description: 'Compute virtualization and VM management.' },
  { key: 'storage', label: 'Storage', description: 'Block, file, and object storage services.' },
  { key: 'kubernetes', label: 'Kubernetes', description: 'Managed Kubernetes clusters.' },
  { key: 'ai', label: 'AI Services', description: 'AI/ML workloads and GPU acceleration.' },
  { key: 'mail', label: 'Mail Services', description: 'Email and messaging services.' },
]

export const ServiceIcon = ({ serviceKey }: { serviceKey: string }) => {
  const Icon = serviceKey === 'hypervisor'
    ? Server
    : serviceKey === 'storage'
      ? Database
      : serviceKey === 'kubernetes'
        ? Layers3
        : serviceKey === 'ai'
          ? Cpu
          : serviceKey === 'mail'
            ? PackageCheck
            : Clock
  return <Icon className="size-4 text-muted-foreground/80" />
}

interface ZoneServicesSectionProps {
  zoneID: string
  enabledServices: ZoneServiceHealth[]
  zoneStatus: string
  onRefresh: () => void
}

async function readErrorMessage(response: Response) {
  try {
    const payload = await response.json()
    return payload.message || payload.error || 'Cannot update service configuration'
  } catch {
    return 'Cannot update service configuration'
  }
}

export default function ZoneServicesSection({
  zoneID,
  enabledServices,
  zoneStatus,
  onRefresh,
}: ZoneServicesSectionProps) {
  const isEditable = zoneStatus === 'maintenance'

  // [COMMENT]: Quản lý trạng thái xác thực mã OTP & ký số nội bộ tại Section Component
  const [pendingServiceToggle, setPendingServiceToggle] = useState<{ key: string; enabled: boolean } | null>(null)
  const [isOTPOpen, setIsOTPOpen] = useState(false)
  const [signing, setSigning] = useState(false)
  const [localError, setLocalError] = useState('')

  const handleToggleService = (serviceType: string, enabled: boolean) => {
    setLocalError('')
    setPendingServiceToggle({ key: serviceType, enabled })
    setIsOTPOpen(true)
  }

  const confirmServiceToggle = async (otpCode: string) => {
    if (!pendingServiceToggle) return
    setSigning(true)
    setLocalError('')
    try {
      const deviceKeys = await getOrCreateDeviceKeys()
      if (!deviceKeys.privateKey) {
        throw new Error('Security keys are missing on this device. Please log out and sign in again to register your keys.')
      }

      const bodyString = JSON.stringify({
        zone_id: zoneID,
        service_type: pendingServiceToggle.key,
        enabled: pendingServiceToggle.enabled,
      })
      const bodyHash = await sha256Hex(bodyString)
      const timestamp = Math.floor(Date.now() / 1000).toString()
      const nonce = generateNonce()
      const path = `/admin/critical/core/zones/services`
      const payloadStr = `PUT\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`
      const signature = await signPayload(payloadStr, deviceKeys.privateKey)

      const response = await Fetch(path, {
        method: 'PUT',
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
        throw new Error(await readErrorMessage(response))
      }

      setIsOTPOpen(false)
      setPendingServiceToggle(null)
      onRefresh()
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : 'Cannot update service configuration')
    } finally {
      setSigning(false)
    }
  }

  // [COMMENT]: Map mã màu và nhãn hiển thị cho Actual State thực tế của dịch vụ
  const getActualServiceStatus = (actualState?: string, isEnabled?: boolean) => {
    if (!isEnabled) {
      return { label: 'Inactive', dotClass: 'bg-slate-400', textClass: 'text-slate-500' }
    }
    switch ((actualState || '').toLowerCase()) {
      case 'healthy':
        return { label: 'Healthy', dotClass: 'bg-emerald-500', textClass: 'text-emerald-600' }
      case 'degraded':
        return { label: 'Degraded', dotClass: 'bg-amber-500', textClass: 'text-amber-600' }
      case 'unhealthy':
        return { label: 'Unhealthy', dotClass: 'bg-rose-500', textClass: 'text-rose-600' }
      case 'down':
        return { label: 'Down', dotClass: 'bg-red-500', textClass: 'text-red-600' }
      default:
        return { label: 'Unknown', dotClass: 'bg-slate-300', textClass: 'text-muted-foreground' }
    }
  }

  return (
    <div id="zone-services-section" className="border border-border bg-card rounded-lg overflow-hidden shadow-[0_1px_2px_rgba(0,0,0,0.02)] p-5 space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-bold text-foreground">Zone Services Configuration</h3>
        {!isEditable && (
          <div className="flex items-center gap-1.5 rounded-md bg-amber-500/10 px-2 py-1 text-[11px] font-medium text-amber-700 ring-1 ring-inset ring-amber-600/20">
            <AlertTriangle className="size-3" />
            <span>Read-only: Zone must be in Maintenance mode to configure services</span>
          </div>
        )}
      </div>

      {localError && (
        <div className="rounded-md bg-red-500/10 p-3 text-xs text-red-600 ring-1 ring-inset ring-red-600/20">
          {localError}
        </div>
      )}

      <div className="overflow-x-auto">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b border-border text-muted-foreground font-medium text-left">
              <th className="pb-2.5 font-medium w-1/4">Service</th>
              <th className="pb-2.5 font-medium w-1/6">Config</th>
              <th className="pb-2.5 font-medium w-1/6">Status (Actual)</th>
              <th className="pb-2.5 font-medium">Description</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border/50">
            {fullServiceCatalog.map((svc) => {
              const matched = enabledServices.find((s) => s.key === svc.key)
              // [COMMENT]: Desired State
              const isEnabled = matched ? matched.desired_state === 'enable' : false
              // [COMMENT]: Actual State
              const statusInfo = getActualServiceStatus(matched?.actual_state, isEnabled)

              return (
                <tr key={svc.key} className="hover:bg-accent/5">
                  <td className="py-3 font-semibold text-foreground flex items-center gap-2">
                    <ServiceIcon serviceKey={svc.key} />
                    <span>{svc.label}</span>
                  </td>
                  <td className="py-3">
                    <Switch
                      checked={isEnabled}
                      disabled={!isEditable}
                      onCheckedChange={(checked) => handleToggleService(svc.key, checked)}
                    />
                  </td>
                  <td className="py-3">
                    <div className="flex items-center gap-1.5 font-medium">
                      <span className={cn("size-2 rounded-full", statusInfo.dotClass)} />
                      <span className={statusInfo.textClass}>
                        {statusInfo.label}
                      </span>
                    </div>
                  </td>
                  <td className="py-3 text-muted-foreground">{svc.description}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      <OTPVerificationDialog
        open={isOTPOpen}
        onOpenChange={(open) => {
          if (signing) return
          setIsOTPOpen(open)
          if (!open) {
            setPendingServiceToggle(null)
          }
        }}
        onConfirm={confirmServiceToggle}
        title="Authorize Service Configuration"
        description={`Enabling/disabling service "${pendingServiceToggle?.key}" is a critical operation. Please enter the 6-digit verification code from your authenticator app to authorize this action.`}
        confirmText="Authorize"
        loading={signing}
      />
    </div>
  )
}
