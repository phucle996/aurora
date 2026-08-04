import { useState, useEffect, useCallback } from 'react'
import { Key, Plus, Shield, CheckCircle2, AlertCircle, Copy, Check, Lock, RefreshCcw } from 'lucide-react'
import { toast } from 'sonner'

import { Fetch } from '@/lib/fetch'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { OTPVerificationDialog } from '@/components/zone/OTPVerificationDialog'
import { getOrCreateDeviceKeys, generateNonce, sha256Hex, signPayload } from '@/lib/crypto'

// [COMMENT]: Định nghĩa kiểu dữ liệu cho một Encryption Key theo API contract
export type ZoneEncryptionKey = {
  id: string
  zone_id: string
  public_key: string
  fingerprint: string
  algorithm: string
  status: 'staged' | 'active' | 'decrypt_only' | 'retired'
  registered_by: string
  activated_by?: string
  decrypt_only_by?: string
  retired_by?: string
  created_at: string
  updated_at: string
}

type Props = {
  zoneId: string
}

// [COMMENT]: Component quản lý danh sách và đăng ký Zone Encryption Keys với thiết kế hiện đại
export default function ZoneEncryptionKeysPanel({ zoneId }: Props) {
  const [keys, setKeys] = useState<ZoneEncryptionKey[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // State điều khiển Register Modal & OTP Step-up
  const [isRegisterOpen, setIsRegisterOpen] = useState(false)
  const [publicKeyInput, setPublicKeyInput] = useState('')
  const [isOTPOpen, setIsOTPOpen] = useState(false)
  const [signing, setSigning] = useState(false)

  // State lưu trữ key đang được chọn để Activate hoặc Retire
  const [pendingAction, setPendingAction] = useState<{
    type: 'activate' | 'retire'
    key: ZoneEncryptionKey
  } | null>(null)

  // State phục vụ nút Copy to Clipboard
  const [copiedId, setCopiedId] = useState<string | null>(null)

  // [COMMENT]: Hàm fetch danh sách keys từ backend REST API
  const fetchKeys = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const response = await Fetch(`/admin/critical/hierarchy/zones/${encodeURIComponent(zoneId)}/encryption-keys?limit=50`)
      if (!response.ok) {
        if (response.status === 404) {
          setKeys([])
          return
        }
        throw new Error('Failed to load encryption keys')
      }
      const data = await response.json()
      setKeys(data.data?.items || data.items || [])
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot fetch keys')
    } finally {
      setLoading(false)
    }
  }, [zoneId])

  useEffect(() => {
    fetchKeys()
  }, [fetchKeys])

  // [COMMENT]: Nút copy nhanh Public Key hoặc Fingerprint
  const handleCopy = (text: string, id: string) => {
    navigator.clipboard.writeText(text)
    setCopiedId(id)
    toast.success('Copied to clipboard')
    setTimeout(() => setCopiedId(null), 2000)
  }

  // [COMMENT]: Mở OTP Dialog khi click Register
  const handleTriggerRegisterOTP = () => {
    if (!publicKeyInput.trim()) {
      toast.error('Please enter a valid Base64 Public Key')
      return
    }
    setIsOTPOpen(true)
  }

  // [COMMENT]: Thực thi Đăng ký Public Key mới với Ed25519 signature & Step-up OTP
  const executeRegisterKey = async (otpCode: string) => {
    setSigning(true)
    try {
      const deviceKeys = await getOrCreateDeviceKeys()
      if (!deviceKeys.privateKey) {
        throw new Error('Security keys missing on this device. Log out and in again.')
      }

      const bodyString = JSON.stringify({
        public_key: publicKeyInput.trim(),
      })
      const bodyHash = await sha256Hex(bodyString)
      const timestamp = Math.floor(Date.now() / 1000).toString()
      const nonce = generateNonce()
      const path = `/admin/critical/hierarchy/zones/${zoneId}/encryption-keys`

      // Format: METHOD\nPATH\nQUERY\nBODY_HASH_HEX\nTIMESTAMP\nNONCE
      const payloadStr = `POST\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`
      const signature = await signPayload(payloadStr, deviceKeys.privateKey)

      const response = await Fetch(path, {
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
        const errPayload = await response.json().catch(() => ({}))
        throw new Error(errPayload.message || errPayload.error || 'Failed to register key')
      }

      toast.success('Zone encryption key registered successfully!')
      setIsOTPOpen(false)
      setIsRegisterOpen(false)
      setPublicKeyInput('')
      fetchKeys()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : 'Registration failed')
    } finally {
      setSigning(false)
    }
  }

  // [COMMENT]: Kích hoạt (Activate) Key đã đăng ký
  const executeActionKey = async (otpCode: string) => {
    if (!pendingAction) return
    setSigning(true)
    const { type, key } = pendingAction
    try {
      const deviceKeys = await getOrCreateDeviceKeys()
      if (!deviceKeys.privateKey) {
        throw new Error('Security keys missing on this device.')
      }

      const bodyString = JSON.stringify({})
      const bodyHash = await sha256Hex(bodyString)
      const timestamp = Math.floor(Date.now() / 1000).toString()
      const nonce = generateNonce()
      const path = `/admin/critical/hierarchy/zones/${zoneId}/encryption-keys/${key.id}/${type}`

      const payloadStr = `POST\n${path}\n\n${bodyHash}\n${timestamp}\n${nonce}`
      const signature = await signPayload(payloadStr, deviceKeys.privateKey)

      const response = await Fetch(path, {
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
        const errPayload = await response.json().catch(() => ({}))
        throw new Error(errPayload.message || errPayload.error || `Failed to ${type} key`)
      }

      toast.success(`Key ${type}d successfully!`)
      setIsOTPOpen(false)
      setPendingAction(null)
      fetchKeys()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : `Action ${type} failed`)
    } finally {
      setSigning(false)
    }
  }

  return (
    <div className="space-y-6">
      {/* Header Bar */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 p-5 rounded-2xl bg-slate-900/60 border border-slate-800/80 backdrop-blur-xl shadow-2xl">
        <div className="flex items-center space-x-3">
          <div className="p-2.5 rounded-xl bg-indigo-500/10 border border-indigo-500/20 text-indigo-400">
            <Key className="w-5 h-5" />
          </div>
          <div>
            <h3 className="text-lg font-semibold text-slate-100 flex items-center gap-2">
              Zone Encryption Keys (X25519)
              <Badge variant="outline" className="bg-emerald-500/10 text-emerald-400 border-emerald-500/20 text-xs">
                Zero-Trust Protected
              </Badge>
            </h3>
            <p className="text-xs text-slate-400">
              Mã hóa E2EE cho tất cả các payload công việc gửi từ Central xuống Zone
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            onClick={fetchKeys}
            className="text-slate-400 hover:text-slate-200 hover:bg-slate-800/50"
          >
            <RefreshCcw className={cn("w-4 h-4 mr-1.5", loading && "animate-spin")} />
            Refresh
          </Button>

          <Button
            onClick={() => setIsRegisterOpen(true)}
            size="sm"
            className="bg-indigo-600 hover:bg-indigo-500 text-white font-medium shadow-lg shadow-indigo-600/20"
          >
            <Plus className="w-4 h-4 mr-1.5" />
            Register Public Key
          </Button>
        </div>
      </div>

      {/* Keys List Section */}
      {loading ? (
        <div className="space-y-3">
          <Skeleton className="h-24 w-full bg-slate-800/50 rounded-xl" />
          <Skeleton className="h-24 w-full bg-slate-800/50 rounded-xl" />
        </div>
      ) : error ? (
        <div className="p-4 rounded-xl bg-rose-500/10 border border-rose-500/20 text-rose-400 flex items-center gap-3 text-sm">
          <AlertCircle className="w-5 h-5 shrink-0" />
          <span>{error}</span>
        </div>
      ) : keys.length === 0 ? (
        <div className="p-12 text-center rounded-2xl bg-slate-900/40 border border-dashed border-slate-800">
          <Shield className="w-10 h-10 mx-auto text-slate-600 mb-3" />
          <h4 className="text-sm font-medium text-slate-300">Chưa có Encryption Key nào được đăng ký</h4>
          <p className="text-xs text-slate-500 mt-1 max-w-md mx-auto">
            Sinh cặp khóa X25519 bằng script <code className="text-indigo-400 bg-slate-800 px-1.5 py-0.5 rounded">python3 scripts/gen-zone-keyring.py</code> và nhấn Đăng ký bên trên.
          </p>
          <Button
            onClick={() => setIsRegisterOpen(true)}
            size="sm"
            variant="outline"
            className="mt-4 border-indigo-500/30 text-indigo-400 hover:bg-indigo-500/10"
          >
            <Plus className="w-4 h-4 mr-1.5" />
            Đăng ký Key đầu tiên
          </Button>
        </div>
      ) : (
        <div className="space-y-3">
          {keys.map((k) => {
            const isActive = k.status === 'active'
            const isStaged = k.status === 'staged'

            return (
              <div
                key={k.id}
                className={cn(
                  "p-5 rounded-2xl border transition-all duration-200",
                  isActive
                    ? "bg-slate-900/80 border-emerald-500/30 shadow-lg shadow-emerald-500/5"
                    : isStaged
                      ? "bg-slate-900/60 border-amber-500/30"
                      : "bg-slate-900/40 border-slate-800 opacity-75"
                )}
              >
                <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-4">
                  <div className="space-y-2">
                    <div className="flex items-center gap-3">
                      <Badge
                        className={cn(
                          "uppercase text-[10px] font-bold tracking-wider px-2 py-0.5 rounded-md border",
                          isActive && "bg-emerald-500/10 text-emerald-400 border-emerald-500/30",
                          isStaged && "bg-amber-500/10 text-amber-400 border-amber-500/30",
                          k.status === 'decrypt_only' && "bg-blue-500/10 text-blue-400 border-blue-500/30",
                          k.status === 'retired' && "bg-slate-800 text-slate-400 border-slate-700"
                        )}
                      >
                        {k.status}
                      </Badge>
                      <span className="text-xs font-mono text-slate-400">ID: {k.id}</span>
                      <span className="text-xs text-slate-500">• {k.algorithm || 'X25519'}</span>
                    </div>

                    {/* Fingerprint */}
                    <div className="flex items-center gap-2 text-xs">
                      <span className="text-slate-500 font-medium">Fingerprint (SHA-256):</span>
                      <code className="font-mono text-slate-300 bg-slate-950/80 px-2 py-0.5 rounded border border-slate-800/80 select-all">
                        {k.fingerprint}
                      </code>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-6 w-6 text-slate-400 hover:text-slate-200"
                        onClick={() => handleCopy(k.fingerprint, `fp-${k.id}`)}
                      >
                        {copiedId === `fp-${k.id}` ? <Check className="w-3 h-3 text-emerald-400" /> : <Copy className="w-3 h-3" />}
                      </Button>
                    </div>

                    {/* Public Key */}
                    <div className="flex items-center gap-2 text-xs">
                      <span className="text-slate-500 font-medium">Public Key (Base64):</span>
                      <code className="font-mono text-indigo-300 bg-slate-950/80 px-2 py-0.5 rounded border border-slate-800/80 truncate max-w-md">
                        {k.public_key}
                      </code>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-6 w-6 text-slate-400 hover:text-slate-200"
                        onClick={() => handleCopy(k.public_key, `pk-${k.id}`)}
                      >
                        {copiedId === `pk-${k.id}` ? <Check className="w-3 h-3 text-emerald-400" /> : <Copy className="w-3 h-3" />}
                      </Button>
                    </div>
                  </div>

                  {/* Actions */}
                  <div className="flex items-center gap-2 shrink-0">
                    {isStaged && (
                      <Button
                        size="sm"
                        onClick={() => {
                          setPendingAction({ type: 'activate', key: k })
                          setIsOTPOpen(true)
                        }}
                        className="bg-emerald-600 hover:bg-emerald-500 text-white font-medium text-xs"
                      >
                        <CheckCircle2 className="w-3.5 h-3.5 mr-1" />
                        Activate Key
                      </Button>
                    )}

                    {isActive && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => {
                          setPendingAction({ type: 'retire', key: k })
                          setIsOTPOpen(true)
                        }}
                        className="border-rose-500/30 text-rose-400 hover:bg-rose-500/10 text-xs"
                      >
                        <Lock className="w-3.5 h-3.5 mr-1" />
                        Retire Key
                      </Button>
                    )}
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* Dialog Đăng Ký Key Mới */}
      <Dialog open={isRegisterOpen} onOpenChange={setIsRegisterOpen}>
        <DialogContent className="bg-slate-900 border-slate-800 text-slate-100 max-w-lg">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2 text-lg">
              <Key className="w-5 h-5 text-indigo-400" />
              Đăng Ký Zone Encryption Key Mới
            </DialogTitle>
            <DialogDescription className="text-xs text-slate-400">
              Nhập chuỗi X25519 Public Key (Base64) thu được từ lệnh <code className="text-indigo-400">python3 scripts/gen-zone-keyring.py</code>.
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-3">
            <div className="space-y-2">
              <Label className="text-xs text-slate-300 font-medium">Public Key (Standard Base64 - 44 chars)</Label>
              <Input
                placeholder="Ví dụ: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
                value={publicKeyInput}
                onChange={(e) => setPublicKeyInput(e.target.value)}
                className="bg-slate-950 border-slate-800 font-mono text-xs text-indigo-300 focus:border-indigo-500"
              />
            </div>

            <div className="p-3 rounded-lg bg-indigo-500/10 border border-indigo-500/20 text-xs text-indigo-300">
              ℹ️ Sau khi đăng ký, Key sẽ chuyển sang trạng thái <strong>staged</strong>. Bạn có thể nhấn <strong>Activate</strong> để chính thức áp dụng mã hóa.
            </div>
          </div>

          <DialogFooter>
            <Button
              variant="ghost"
              onClick={() => setIsRegisterOpen(false)}
              className="text-slate-400 hover:text-slate-200"
            >
              Hủy
            </Button>
            <Button
              onClick={handleTriggerRegisterOTP}
              disabled={!publicKeyInput.trim()}
              className="bg-indigo-600 hover:bg-indigo-500 text-white font-medium text-xs"
            >
              Tiếp tục (Yêu cầu OTP 2FA)
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* OTP Step-up Verification Dialog */}
      <OTPVerificationDialog
        open={isOTPOpen}
        onOpenChange={setIsOTPOpen}
        onConfirm={pendingAction ? executeActionKey : executeRegisterKey}
        loading={signing}
        title={pendingAction ? `Xác thực 2FA để ${pendingAction.type} Key` : 'Xác thực 2FA để Đăng Ký Encryption Key'}
        description="Đòn bẩy bảo mật SRE Critical Action: Nhập mã 6 chữ số từ ứng dụng Authenticator của bạn."
      />
    </div>
  )
}
