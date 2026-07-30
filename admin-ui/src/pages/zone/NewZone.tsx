import { useState } from 'react'
import { Link, useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'

import { Fetch } from '@/lib/fetch'
import { slugify } from '@/lib/slugify'
import { PageContent } from '@/components/layout/layout'
import { type ZoneLocation } from '@/components/zone/location-autocomplete'
import { getOrCreateDeviceKeys, generateNonce, sha256Hex, signPayload } from '@/lib/crypto'
import { OTPVerificationDialog } from '@/components/zone/OTPVerificationDialog'

import ZoneForm, { type ServiceKey } from './sections/ZoneForm'
import ZonePreviewCard from './sections/ZonePreviewCard'

/**
 * Đọc thông báo lỗi từ API response JSON.
 * Fallback về 'Request failed' nếu body không parse được hoặc thiếu field.
 *
 * @param response - Response object từ Fetch client (đã strip XSSI prefix)
 */
async function readAPIMessage(response: Response) {
  try {
    const payload = (await response.json()) as { message?: string; error?: string }
    return payload.message || payload.error || 'Request failed'
  } catch {
    return 'Request failed'
  }
}

// ---------------------------------------------------------------------------
// Page component
// ---------------------------------------------------------------------------

export default function NewZonePage() {
  const router = useRouter()

  // ---------------------------------------------------------------------------
  // Form state
  // ---------------------------------------------------------------------------

  const [zoneName, setZoneName] = useState('')
  const [zoneCode, setZoneCode] = useState('')

  /**
   * Track xem user đã tự tay sửa zone code chưa.
   * Nếu chưa → auto-populate zone code từ location suggestion.
   * Nếu rồi → giữ nguyên, không ghi đè.
   */
  const [isZoneCodeManuallyEdited, setIsZoneCodeManuallyEdited] = useState(false)
  const [location, setLocation] = useState('')
  const [description, setDescription] = useState('')

  /**
   * Services được enable khi zone này được tạo.
	 * Default: hypervisor + storage + k8s bật sẵn (baseline services).
   * mail và ai tắt mặc định vì cần cấu hình riêng biệt.
   */
  const [services, setServices] = useState<Record<ServiceKey, boolean>>({
    hypervisor: true,
    storage: true,
    mail: false,
    k8s: true,
    ai: false,
    managed_service: false,
  })

  // ---------------------------------------------------------------------------
  // OTP step-up state
  // ---------------------------------------------------------------------------

  /** true khi OTP dialog đang mở */
  const [isOTPOpen, setIsOTPOpen] = useState(false)

  /** true khi đang trong quá trình sign + submit — disable các nút để tránh double submit */
  const [signing, setSigning] = useState(false)

  // ---------------------------------------------------------------------------
  // Derived state
  // ---------------------------------------------------------------------------

  const trimmedName = zoneName.trim()
  const trimmedCode = zoneCode.trim()
  const trimmedLocation = location.trim()

  /** true khi đủ điều kiện submit: tên, code, location đều có và không đang sign */
  const canSubmit = trimmedName !== '' && trimmedCode !== '' && trimmedLocation !== '' && !signing

  // ---------------------------------------------------------------------------
  // Event handlers
  // ---------------------------------------------------------------------------

  /** Toggle service on/off khi user click vào service card */
  const toggleService = (key: ServiceKey) => {
    setServices((current) => ({ ...current, [key]: !current[key] }))
  }

  /**
   * Khi user chọn location từ autocomplete:
   *   1. Set location label
   *   2. Nếu zone code chưa được chỉnh tay → auto-fill từ location suggestion
   */
  const selectLocation = (item: ZoneLocation) => {
    setLocation(item.label)
    if (!zoneCode.trim()) {
      setZoneCode(item.suggestedCode)
    }
  }

  /**
   * Validate form và mở OTP dialog.
   * Không submit trực tiếp — phải qua bước xác thực TOTP.
   */
  const handleTriggerOTP = () => {
    if (!canSubmit) {
      toast.error('Please fill in zone name, code, and location before creating the zone.')
      return
    }
    setIsOTPOpen(true)
  }

  const confirmCreateZoneWithOTP = async (otpCode: string) => {

    setSigning(true)
    try {
      // Lấy device keys từ IndexedDB.
      // getOrCreateDeviceKeys() sẽ tạo mới nếu chưa có (lần đầu đăng nhập thiết bị).
      // privateKey là non-extractable CryptoKey — không thể serialize ra JS string.
      const deviceKeys = await getOrCreateDeviceKeys()
      if (!deviceKeys.privateKey) {
        throw new Error('Security keys are missing on this device. Please log out and sign in again to register your keys.')
      }

      // Serialize body thành JSON string để hash.
      // slugifyZoneCode đảm bảo zone code luôn lowercase + no special chars.
      const bodyString = JSON.stringify({
        name: trimmedName,
        code: slugify(trimmedCode),
        location: trimmedLocation,
        description: description.trim(),
        enable_hypervisor: services.hypervisor,
        enable_storage: services.storage,
        enable_mail: services.mail,
        enable_kubernetes: services.k8s,
        enable_ai: services.ai,
        enable_managed_service: services.managed_service,
      })

      // SHA-256 hex của body — đưa vào payload để backend verify body không bị tamper.
      const bodyHash = await sha256Hex(bodyString)

      // Unix timestamp (seconds) — backend kiểm tra nằm trong clock skew cho phép.
      const timestamp = Math.floor(Date.now() / 1000).toString()

      // Nonce ngẫu nhiên — backend dùng Redis SETNX để đảm bảo chỉ dùng 1 lần.
      const nonce = generateNonce()

      // Canonical payload string — phải khớp chính xác với backend buildSigPayload.
      // Format: METHOD\nPATH\nQUERY\nBODY_HASH_HEX\nTIMESTAMP\nNONCE
      //
      // accessKey KHÔNG được đưa vào payload vì:
      //   - Backend đọc accessKey từ HttpOnly cookie (gửi kèm request tự động).
      //   - Đưa vào payload → lộ session ID qua log/trace/network capture.
      //   - Binding với session đã được đảm bảo qua device public key lookup.
      const payloadStr = `POST\n/admin/critical/hierarchy/zones\n\n${bodyHash}\n${timestamp}\n${nonce}`

      // Sign payload bằng Ed25519 private key từ IndexedDB.
      // Backend verify bằng device public key đã đăng ký lúc login.
      const signature = await signPayload(payloadStr, deviceKeys.privateKey)

      const response = await Fetch('/admin/critical/hierarchy/zones', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-Admin-Signature': signature,    // Ed25519 signature, base64-encoded
          'X-Admin-Timestamp': timestamp,    // Unix seconds, để backend check clock skew
          'X-Admin-Nonce': nonce,            // Random nonce, Redis SETNX 1 lần
          'X-Admin-StepUp-Code': otpCode,    // TOTP 6-digit, step-up 2FA
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
      // Luôn reset signing state để re-enable UI, tránh user bị kẹt
      setSigning(false)
    }
  }

  return (
    <PageContent className="pb-6">
      {/* Page header: breadcrumb + title + X button */}
      <div className="flex items-center justify-between pb-4">
        <div className="space-y-1">
          <nav className="flex items-center gap-2 text-[13px] font-medium text-slate-500 dark:text-slate-400 mb-2">
            <Link to="/" className="hover:underline">
              Home
            </Link>
            <span className="text-slate-400 font-light">&gt;</span>
            <Link to="/zones" className="hover:underline">
              Zones
            </Link>
            <span className="text-slate-400 font-light">&gt;</span>
            <span className="text-slate-900 dark:text-slate-200 font-semibold">New zone</span>
          </nav>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-bold tracking-tight text-slate-900 dark:text-slate-50">
              New zone
            </h1>
          </div>
          <p className="text-[13px] text-slate-500 dark:text-slate-400">
            Create a new infrastructure zone to organize and deploy your platform resources.
          </p>
        </div>
      </div>

      {/* Form + Preview 2-column layout (bleed edge-to-edge) */}
      <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_380px] -mx-4 md:-mx-6 -mb-4 md:-mb-5 border-t border-slate-200/60 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-950/20">
        <div className="bg-white dark:bg-slate-900 px-4 py-8 md:px-12 border-b xl:border-b-0 xl:border-r border-slate-200/60 dark:border-slate-800">
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
            onSubmit={handleTriggerOTP}
            disabled={!canSubmit}
          />
        </div>
        <div className="px-4 py-8 md:px-8 bg-transparent">
          <ZonePreviewCard
            zoneName={zoneName}
            zoneCode={zoneCode}
            location={location}
            description={description}
            services={services}
          />
        </div>
      </div>

      {/* ---------------------------------------------------------------------------
          OTP Step-Up Dialog
          Mở sau khi form hợp lệ. User nhập TOTP → confirm → sign + submit.
          Dialog không tự close khi signing — user phải đợi kết quả.
      --------------------------------------------------------------------------- */}
      <OTPVerificationDialog
        open={isOTPOpen}
        onOpenChange={setIsOTPOpen}
        onConfirm={confirmCreateZoneWithOTP}
        title="Security Verification"
        description="Zone creation is a critical operation. Please enter the 6-digit verification code from your authenticator app to authorize this action."
        confirmText="Create Zone"
        loading={signing}
      />
    </PageContent>
  )
}
