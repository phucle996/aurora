import { type FormEvent, useEffect, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { toast } from 'sonner'
import { useTranslation } from 'react-i18next'
import {
  KeyRound,
  Lock,
} from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Fetch } from '@/lib/fetch'
import { usePageMeta } from '@/lib/page-meta'
import { useAdminSession } from '@/hooks/useAdminSession'
import AuthLayout from './AuthLayout'

type AdminLoginState = {
  loading: boolean
  error: string
}

type MFAMethod = 'totp' | 'recovery_code'

import { getOrCreateDeviceKeys } from '@/lib/crypto'

async function extractBackendError(resp: Response): Promise<string> {
  try {
    const data = await resp.json() as { error?: string; message?: string }
    if (data && typeof data === 'object') {
      return data.message || data.error || 'Admin login failed.'
    }
  } catch {
    // Fallback if parsing fails
  }
  return 'Admin login failed.'
}

export default function AdminAPIKeyLoginPage() {
  const { t } = useTranslation('auth')
  const [theme, setTheme] = useState<'light' | 'dark'>(() => {
    if (typeof document !== 'undefined') {
      return document.documentElement.classList.contains('dark') ? 'dark' : 'light'
    }
    return 'light'
  })

  useEffect(() => {
    const observer = new MutationObserver(() => {
      const isDark = document.documentElement.classList.contains('dark')
      setTheme(isDark ? 'dark' : 'light')
    })

    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class'],
    })

    return () => observer.disconnect()
  }, [])

  usePageMeta(t('metaTitle'), t('metaDesc'))
  const navigate = useNavigate()
  const { refreshSession } = useAdminSession()
  const [apiKey, setAPIKey] = useState('')
  const [twoFactorCode, setTwoFactorCode] = useState('')
  const [mfaMethod, setMFAMethod] = useState<MFAMethod>('totp')
  const [state, setState] = useState<AdminLoginState>({
    loading: false,
    error: '',
  })

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmed = apiKey.trim()
    const trimmedMFACode = twoFactorCode.trim()
    if (trimmed === '') {
      setState({ loading: false, error: t('apiKeyRequired') })
      toast.error(t('apiKeyRequired'))
      return
    }
    if (trimmedMFACode.length < 6) {
      setState({ loading: false, error: '' })
      toast.error(mfaMethod === 'totp' ? t('totpLengthError') : t('recoveryLengthError'))
      return
    }

    setState({ loading: true, error: '' })

    try {
      const deviceKeys = await getOrCreateDeviceKeys()

      // [COMMENT]: Cấu hình Payload chuẩn theo Contract API của Rust acr:
      // - api_key: Plaintext SRE API Key
      // - totp_code: mã 2FA TOTP (6 chữ số)
      // - device_public_key: Khóa công khai Ed25519 của trình duyệt
      // Đăng nhập này luôn tự động vào vùng 'global', không cần/không hỗ trợ chọn zone_code.
      const resp = await Fetch('/admin/auth/login', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          api_key: trimmed,
          totp_code: trimmedMFACode,
          device_public_key: deviceKeys.publicKey,
        }),
      })

      if (resp.ok) {
        const nextSession = await refreshSession()
        if (nextSession.authenticated) {
          await navigate({ to: '/' })
          return
        }
        toast.error(t('loginFailed'))
        setState({
          loading: false,
          error: '',
        })
        return
      }

      const backendError = await extractBackendError(resp)
      toast.error(backendError)
      setState({
        loading: false,
        error: '',
      })
    } catch {
      toast.error(t('cannotConnect'))
      setState({
        loading: false,
        error: '',
      })
    }
  }

  return (
    <AuthLayout>
      <div
        className="rounded-4xl border border-[#dfe7f3] bg-white p-8 shadow-[0_40px_80px_-40px_rgba(15,23,42,0.25)] sm:p-10 dark:border-slate-700/30 dark:bg-[#0f172a]/85 dark:backdrop-blur-md transition-all duration-500 ease-[cubic-bezier(0.22,1,0.36,1)] hover:-translate-y-0.5"
        style={theme === 'dark' ? {
          boxShadow: '0 24px 70px rgba(0, 0, 0, 0.38), inset 0 1px 0 rgba(255, 255, 255, 0.04)'
        } : undefined}
      >
        <div className="mb-8 space-y-2">
          <p className="text-[12px] font-bold uppercase tracking-wider text-primary dark:text-[#60A5FA]">{t('consoleTitle')}</p>
          <h2 className="m-0 font-black text-[24px] tracking-tight text-[#0b1730] sm:text-[34px] dark:text-slate-50">{t('loginHeading')}</h2>
          <p className="text-lg leading-relaxed text-[#5f6e86] dark:text-[#CBD5E1]">
            {t('loginDesc')}
          </p>
        </div>

        <form className="space-y-6" onSubmit={onSubmit}>
          <div className="space-y-2.5">
            <label htmlFor="api-key" className="text-sm font-bold text-[#334155] dark:text-slate-200">
              {t('apiKeyLabel')}
            </label>
            <div className="group relative">
              <KeyRound className="absolute left-4 top-1/2 h-5 w-5 -translate-y-1/2 text-[#95a2b8] transition-colors group-focus-within:text-primary dark:group-focus-within:text-[#60A5FA]" />
              <Input
                id="api-key"
                type="password"
                placeholder={t('apiKeyPlaceholder')}
                value={apiKey}
                onChange={(event) => setAPIKey(event.target.value)}
                disabled={state.loading}
                className="h-14 w-full rounded-xl border-[#dbe5f2] bg-[#f8fbff] pl-12 text-base font-semibold text-[#1e293b] shadow-none ring-offset-white transition-all placeholder:font-medium placeholder:text-[#94a3b8] hover:border-primary/30 focus-visible:border-primary focus-visible:bg-white focus-visible:ring-4 focus-visible:ring-primary/10 disabled:opacity-70 dark:border-slate-700/40 dark:bg-[#0f172a]/70 dark:text-slate-100 dark:placeholder:text-slate-500 dark:focus-visible:border-blue-500 dark:focus-visible:bg-[#0f172a] dark:focus-visible:ring-blue-500/20"
              />
            </div>
          </div>

          <div className="space-y-2.5">
            <label htmlFor="two-factor-code" className="text-sm font-bold text-[#334155] dark:text-slate-200">
              {mfaMethod === 'totp' ? t('totpCodeLabel') : t('recoveryCodeLabel')}
            </label>
            <div className="group flex h-14 w-full overflow-hidden rounded-xl border border-[#dbe5f2] bg-[#f8fbff] transition-all hover:border-primary/30 focus-within:border-primary focus-within:bg-white focus-within:ring-4 focus-within:ring-primary/10 dark:border-slate-700/40 dark:bg-[#0f172a]/70 dark:focus-within:border-blue-500 dark:focus-within:bg-[#0f172a] dark:focus-within:ring-blue-500/20">
              <div className="h-full w-30 shrink-0 border-r border-[#dbe5f2] sm:w-33 dark:border-slate-700/40">
                <Select value={mfaMethod} onValueChange={(value) => setMFAMethod(value as MFAMethod)} disabled={state.loading}>
                  <SelectTrigger className="h-14! w-full items-center rounded-none border-0 bg-transparent py-0! pl-3 pr-2 text-sm font-semibold leading-none text-[#334155] shadow-none focus-visible:ring-0 dark:text-slate-200">
                    <SelectValue placeholder={mfaMethod === 'totp' ? t('mfaMethodTotp') : t('mfaMethodRecovery')} className="leading-none" />
                  </SelectTrigger>
                  <SelectContent position="popper" align="start" className="min-w-33 dark:bg-[#0f172a] dark:border-slate-800">
                    <SelectItem value="totp" className="dark:text-slate-200">{t('mfaMethodTotp')}</SelectItem>
                    <SelectItem value="recovery_code" className="dark:text-slate-200">{t('mfaMethodRecovery')}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <Input
                id="two-factor-code"
                inputMode={mfaMethod === 'totp' ? 'numeric' : 'text'}
                autoComplete="one-time-code"
                placeholder={mfaMethod === 'totp' ? t('totpPlaceholder') : t('recoveryPlaceholder')}
                value={twoFactorCode}
                onChange={(event) => setTwoFactorCode(event.target.value)}
                disabled={state.loading}
                className="h-full w-full border-0 bg-transparent py-0 pl-3 text-[15px] font-semibold leading-none text-[#1e293b] shadow-none ring-0 placeholder:font-medium placeholder:text-[#94a3b8] focus-visible:ring-0 disabled:opacity-70 dark:text-slate-100 dark:placeholder:text-slate-500"
              />
            </div>
          </div>

          <div className="flex flex-wrap items-center justify-end gap-3">
            <button type="button" className="cursor-pointer text-sm font-bold text-primary transition-colors hover:text-primary/80 dark:text-blue-400 dark:hover:text-blue-300">
              {t('whereToFindKeys')}
            </button>
          </div>

          <div className="space-y-3 pt-1">
            <Button
              type="submit"
              disabled={state.loading}
              className="group h-14 w-full rounded-xl bg-primary text-base font-bold text-white shadow-lg shadow-primary/25 transition-all hover:-translate-y-px hover:bg-primary/90 hover:shadow-xl hover:shadow-primary/30 active:translate-y-0 disabled:translate-y-0 disabled:opacity-70 dark:bg-linear-to-r dark:from-[#2563EB] dark:to-[#3B82F6] dark:text-white dark:shadow-[0_14px_32px_rgba(37,99,235,0.30)] dark:hover:from-[#1D4ED8] dark:hover:to-[#2563EB] dark:hover:shadow-[0_16px_36px_rgba(37,99,235,0.36)]"
            >
              <Lock className="mr-2 h-4 w-4 transition-transform group-hover:scale-110" />
              {state.loading ? t('btnAuthenticating') : t('btnAccessConsole')}
            </Button>
          </div>
        </form>
      </div>

      <p className="pt-5 text-center text-sm font-medium text-[#5f6e86] dark:text-slate-400">
        {t('needHelp')}{' '}
        <button className="cursor-pointer text-primary transition-colors hover:text-primary/80 dark:text-blue-400 dark:hover:text-blue-300">{t('viewDocs')}</button> {t('or')}{' '}
        <button className="cursor-pointer text-primary transition-colors hover:text-primary/80 dark:text-blue-400 dark:hover:text-blue-300">{t('contactSupport')}</button>
      </p>
    </AuthLayout>
  )
}
