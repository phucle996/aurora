/**
 * NewZone.tsx — Trang tạo Zone mới trong Aurora Admin Console.
 *
 * Zone là root topology node của toàn bộ hạ tầng — mọi dataplane, routing
 * và service discovery đều gắn với Zone. Tạo Zone là critical operation,
 * yêu cầu xác thực 3 lớp:
 *
 *   Lớp 1 — Admin API Key (cookie): AdminAPIKeyAuth middleware verify token.
 *   Lớp 2 — Ed25519 Signature: request phải được ký bằng device private key
 *            (lưu trong IndexedDB dưới dạng non-extractable CryptoKey).
 *   Lớp 3 — TOTP Step-Up 2FA: user nhập OTP từ authenticator app.
 *
 * Signature payload format (phải khớp chính xác với backend buildSigPayload):
 *
 *   METHOD\nPATH\nQUERY\nBODY_SHA256_HEX\nTIMESTAMP_UNIX\nNONCE
 *
 *   Lưu ý: accessKey KHÔNG được đưa vào payload —
 *     - Backend đọc accessKey từ HttpOnly cookie (gửi kèm request tự động).
 *     - Đưa accessKey vào payload string sẽ lộ session ID qua log/trace.
 *     - Signature bind với session thông qua device public key lookup dùng accessKey.
 *
 * Security design:
 *   - Private key lưu trong IndexedDB với extractable: false → không thể export ra JS.
 *   - Nonce được dùng 1 lần (Redis SETNX) → chống replay attack.
 *   - Timestamp trong clock skew (configurable, mặc định 5 phút) → chống stale request.
 *   - TOTP step-up tách biệt khỏi session auth → compromise session không đủ để tạo zone.
 *
 * Flow:
 *   1. User điền form (name, code, location, description, services).
 *   2. Click "Create Zone" → validate form → mở OTP dialog.
 *   3. User nhập TOTP → confirm → sign + submit.
 *   4. Backend verify: AdminAPIKeyAuth → Signature → StepUp2FA → CreateZone handler.
 *   5. Success → navigate về /zones. Error → toast error.
 */

import { useState } from 'react'
import { Link, useRouter } from '@tanstack/react-router'
import { toast } from 'sonner'


import { Fetch } from '@/lib/fetch'
import { slugify } from '@/lib/slugify'
import { PageContent } from '@/components/layout/layout'
import { Button } from '@/components/ui/button'
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
   * Default: hypervisor + storage + k8s bật sẵn (core services).
   * mail và ai tắt mặc định vì cần cấu hình riêng biệt.
   */
  const [services, setServices] = useState<Record<ServiceKey, boolean>>({
    hypervisor: true,
    storage: true,
    mail: false,
    k8s: true,
    ai: false,
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

  /**
   * Xác nhận tạo zone sau khi user nhập OTP thành công.
   *
   * Security flow:
   *   1. Lấy device private key từ IndexedDB (non-extractable, không thể copy).
   *   2. Serialize request body thành JSON string.
   *   3. Hash body bằng SHA-256 để đưa vào payload — chống body tampering.
   *   4. Tạo nonce ngẫu nhiên — backend verify 1 lần duy nhất (Redis SETNX).
   *   5. Build canonical payload string theo format đã thống nhất với backend.
   *   6. Sign payload bằng Ed25519 private key.
   *   7. Gửi request với signature + timestamp + nonce + OTP trong headers.
   *   8. Backend verify theo thứ tự: AdminAPIKeyAuth → Signature → StepUp2FA.
   *
   * Error handling:
   *   - Mọi lỗi đều catch và hiển thị qua toast.error().
   *   - setSigning(false) trong finally để re-enable UI dù success hay fail.
   */
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
        enable_k8s: services.k8s,
        enable_ai: services.ai,
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
      const payloadStr = `POST\n/admin/core/zones\n\n${bodyHash}\n${timestamp}\n${nonce}`

      // Sign payload bằng Ed25519 private key từ IndexedDB.
      // Backend verify bằng device public key đã đăng ký lúc login.
      const signature = await signPayload(payloadStr, deviceKeys.privateKey)

      const response = await Fetch('/admin/core/zones', {
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

  // ---------------------------------------------------------------------------
  // Render
  // ---------------------------------------------------------------------------

  return (
    <PageContent className="pb-0">
      {/* Page header: breadcrumb + title + action buttons */}
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
          {/* canSubmit = name + code + location filled && không đang sign */}
          <Button
            className="h-12 rounded-lg px-8 text-sm font-semibold shadow-sm"
            onClick={handleTriggerOTP}
            disabled={!canSubmit}
          >
            {signing ? 'Creating...' : 'Create Zone'}
          </Button>
        </div>
      </div>

      {/* Form + Preview 2-column layout (xl breakpoint) */}
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
        {/* Live preview — cập nhật realtime khi user gõ */}
        <ZonePreviewCard
          zoneName={zoneName}
          location={location}
          description={description}
        />
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
