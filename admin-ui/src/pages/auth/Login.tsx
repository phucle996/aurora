import { useState } from 'react'
import type { FormEvent } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { toast } from 'sonner'
import {
  Cloud,
  KeyRound,
  Lock,
  Shield,
  ShieldCheck,
  Users,
  Zap,
} from 'lucide-react'

import bgLogin from '../../assets/image.png'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Fetch } from '@/lib/fetch'
import { usePageMeta } from '@/lib/page-meta'
import { useAdminSession } from '@/hooks/useAdminSession'

type AdminLoginState = {
  loading: boolean
  error: string
}

type MFAMethod = 'totp' | 'recovery_code'

const adminDevicePublicKeyStorageKey = 'admin.device.public_key.v1'

function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte)
  })
  return btoa(binary)
}

async function getOrCreateDevicePublicKey(): Promise<string> {
  if (typeof window !== 'undefined') {
    const cached = window.localStorage.getItem(adminDevicePublicKeyStorageKey)?.trim() ?? ''
    if (cached !== '') {
      return cached
    }
  }

  if (!crypto?.subtle) {
    throw new Error('WebCrypto is unavailable in this browser.')
  }

  const keyPair = await crypto.subtle.generateKey(
    { name: 'Ed25519' },
    true,
    ['sign', 'verify'],
  )

  const rawPublicKey = await crypto.subtle.exportKey('raw', keyPair.publicKey)
  const encoded = bytesToBase64(new Uint8Array(rawPublicKey))

  if (typeof window !== 'undefined') {
    window.localStorage.setItem(adminDevicePublicKeyStorageKey, encoded)
  }

  return encoded
}

async function extractBackendError(resp: Response): Promise<string> {
  try {
    await resp.body?.cancel()
  } catch {
    // Keep all admin auth failures generic.
  }
  return 'Admin login failed.'
}

export default function AdminAPIKeyLoginPage() {
  usePageMeta('Admin Login | Aurora Cloud', 'Secure admin access for Aurora Cloud controlplane.')
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
      setState({ loading: false, error: 'Admin API key is required.' })
      toast.error('Admin API key is required.')
      return
    }
    if (trimmedMFACode.length < 6) {
      setState({ loading: false, error: '' })
      toast.error(mfaMethod === 'totp' ? 'TOTP code must be at least 6 characters.' : 'Recovery code must be at least 6 characters.')
      return
    }

    setState({ loading: true, error: '' })

    try {
      const devicePublicKey = await getOrCreateDevicePublicKey()

      const resp = await Fetch('/admin/auth/login', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          admin_api_key: trimmed,
          mfa_method: mfaMethod,
          mfa_code: trimmedMFACode,
          device_public_key: devicePublicKey,
        }),
      })

      if (resp.ok) {
        const nextSession = await refreshSession()
        if (nextSession.authenticated) {
          await navigate({ to: '/' })
          return
        }
        toast.error('Admin login failed.')
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
      toast.error('Cannot connect to IAM.')
      setState({
        loading: false,
        error: '',
      })
    }
  }

  return (
    <div
      className="relative min-h-screen w-full overflow-hidden bg-[#e9eef7] bg-cover bg-center bg-no-repeat text-foreground"
      style={{ backgroundImage: `url(${bgLogin})` }}
    >
      <div className="relative z-10 flex min-h-screen w-full flex-col px-5 pb-8 pt-6 sm:px-12 md:px-20 xl:px-[150px] xl:pb-10">
        <header className="flex flex-wrap items-center justify-between gap-4">
          <div className="flex items-center gap-3 text-[#0f172a]">
            <Cloud className="h-10 w-10 text-primary" strokeWidth={2.2} />
            <p className="text-2xl font-bold sm:text-[40px]">
              Aurora <span className="text-primary">Cloud</span>
            </p>
          </div>

          <div className="inline-flex items-center gap-2 rounded-full border border-[#d7e1f0] bg-white/90 px-4 py-2 text-sm font-semibold text-[#5f6e86] shadow-sm backdrop-blur">
            <ShieldCheck className="h-4 w-4 text-[#5f6e86]" />
            SOC 2 Type II <span className="mx-1">•</span> ISO 27001
          </div>
        </header>

        <main className="mt-8 grid flex-1 gap-8 xl:grid-cols-[minmax(0,1fr)_540px] xl:gap-12">
          <section className="flex flex-col">
            <div className="max-w-[560px]">
              <div className="mt-10 space-y-6 sm:mt-20">
                <h1 className="m-0 text-[19px] leading-[1.2] text-[#0f172a] sm:text-[25px] xl:text-[29px]">
                  <span className="font-semibold">Build. Connect. Scale with </span>
                  <span className="font-semibold text-primary">Aurora Cloud.</span>
                </h1>
                <p className="max-w-[480px] pt-3 text-[14px] leading-relaxed text-[#5f6e86] sm:text-[15px] xl:text-[16px]">
                  The secure, high-performance cloud platform for modern infrastructure and developer teams.
                </p>
              </div>

              <div className="mt-10 divide-y divide-[#dfe6f2] backdrop-blur sm:mt-20 sm:px-4">
                <div className="flex items-center gap-6 py-6">
                  <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl border border-[#dbe6f7] bg-[#eef5ff] text-primary shadow-sm sm:h-20 sm:w-20">
                    <Shield className="h-6 w-6 sm:h-10 sm:w-10" />
                  </div>
                  <div className="space-y-1">
                    <p className="text-[20px] font-bold text-[#0f172a]">Session-bound admin access</p>
                    <p className="text-base leading-relaxed text-[#5f6e86]">
                      Admin keys are used only at login, then protected by server-side sessions.
                    </p>
                  </div>
                </div>

                <div className="flex items-center gap-6 py-6">
                  <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl border border-[#dbe6f7] bg-[#eef5ff] text-primary shadow-sm sm:h-20 sm:w-20">
                    <Zap className="h-10 w-10" />
                  </div>
                  <div className="space-y-1">
                    <p className="text-[20px] font-bold text-[#0f172a]">Fast provisioning</p>
                    <p className="text-base leading-relaxed text-[#5f6e86]">
                      Automate infrastructure and services in seconds with our powerful API.
                    </p>
                  </div>
                </div>

                <div className="flex items-center gap-6 py-6">
                  <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-2xl border border-[#dbe6f7] bg-[#eef5ff] text-primary shadow-sm sm:h-20 sm:w-20">
                    <Users className="h-10 w-10" />
                  </div>
                  <div className="space-y-1">
                    <p className="text-[20px] font-bold text-[#0f172a]">Multi-tenant control</p>
                    <p className="text-base leading-relaxed text-[#5f6e86]">
                      Isolated workspaces with centralized governance and full visibility.
                    </p>
                  </div>
                </div>
              </div>
            </div>

            <div className="mt-8 md:mt-10 xl:mt-auto">
              <div className="inline-flex w-fit items-center gap-3 rounded-xl border border-[#dfe8f4] bg-white px-4 py-3 shadow-sm">
                <Users className="h-5 w-5 text-primary" />
                <span className="text-sm font-medium text-[#5f6e86]">
                  Used by <span className="font-bold text-primary">100+</span> organizations in Production
                </span>
              </div>
            </div>
          </section>

          <section className="flex flex-col justify-center">
            <div className="rounded-[32px] border border-[#dfe7f3] bg-white p-8 shadow-[0_40px_80px_-40px_rgba(15,23,42,0.25)] sm:p-10">
              <div className="mb-8 space-y-2">
                <p className="text-[12px] font-bold uppercase tracking-wider text-primary">Admin Console</p>
                <h2 className="m-0 font-black text-[24px] tracking-tight text-[#0b1730] sm:text-[34px]">Secure Admin Login</h2>
                <p className="text-lg leading-relaxed text-[#5f6e86]">
                  Use your Admin API Key and verification code to create a protected console session.
                </p>
              </div>

              <form className="space-y-6" onSubmit={onSubmit}>
                <div className="space-y-2.5">
                  <label htmlFor="api-key" className="text-sm font-bold text-[#334155]">
                    API Key
                  </label>
                  <div className="group relative">
                    <KeyRound className="absolute left-4 top-1/2 h-5 w-5 -translate-y-1/2 text-[#95a2b8] transition-colors group-focus-within:text-primary" />
                    <Input
                      id="api-key"
                      type="password"
                      placeholder="Enter your API key"
                      value={apiKey}
                      onChange={(event) => setAPIKey(event.target.value)}
                      disabled={state.loading}
                      className="h-14 w-full rounded-xl border-[#dbe5f2] bg-[#f8fbff] pl-12 text-base font-semibold text-[#1e293b] shadow-none ring-offset-white transition-all placeholder:font-medium placeholder:text-[#94a3b8] hover:border-primary/30 focus-visible:border-primary focus-visible:bg-white focus-visible:ring-4 focus-visible:ring-primary/10 disabled:opacity-70"
                    />
                  </div>
                </div>

                <div className="space-y-2.5">
                  <label htmlFor="two-factor-code" className="text-sm font-bold text-[#334155]">
                    {mfaMethod === 'totp' ? 'TOTP code' : 'Recovery code'}
                  </label>
                  <div className="group flex h-14 w-full overflow-hidden rounded-xl border border-[#dbe5f2] bg-[#f8fbff] transition-all hover:border-primary/30 focus-within:border-primary focus-within:bg-white focus-within:ring-4 focus-within:ring-primary/10">
                    <div className="h-full w-[120px] shrink-0 border-r border-[#dbe5f2] sm:w-[132px]">
                      <Select value={mfaMethod} onValueChange={(value) => setMFAMethod(value as MFAMethod)} disabled={state.loading}>
                        <SelectTrigger className="!h-14 w-full items-center rounded-none border-0 bg-transparent !py-0 pl-3 pr-2 text-sm font-semibold leading-none text-[#334155] shadow-none focus-visible:ring-0">
                          <SelectValue placeholder="TOTP" className="leading-none" />
                        </SelectTrigger>
                        <SelectContent position="popper" align="start" className="min-w-[132px]">
                          <SelectItem value="totp">TOTP</SelectItem>
                          <SelectItem value="recovery_code">Recovery</SelectItem>
                        </SelectContent>
                      </Select>
                    </div>
                    <Input
                      id="two-factor-code"
                      inputMode={mfaMethod === 'totp' ? 'numeric' : 'text'}
                      autoComplete="one-time-code"
                      placeholder={mfaMethod === 'totp' ? 'Enter 6-digit authenticator code' : 'Enter recovery code'}
                      value={twoFactorCode}
                      onChange={(event) => setTwoFactorCode(event.target.value)}
                      disabled={state.loading}
                      className="h-full w-full border-0 bg-transparent py-0 pl-3 text-[15px] font-semibold leading-none text-[#1e293b] shadow-none ring-0 placeholder:font-medium placeholder:text-[#94a3b8] focus-visible:ring-0 disabled:opacity-70"
                    />
                  </div>
                </div>

                <div className="flex flex-wrap items-center justify-end gap-3">
                  <button type="button" className="cursor-pointer text-sm font-bold text-primary transition-colors hover:text-primary/80">
                    Where do I find my API keys?
                  </button>
                </div>

                <div className="space-y-3 pt-1">
                  <Button
                    type="submit"
                    disabled={state.loading}
                    className="group h-14 w-full rounded-xl bg-primary text-base font-bold text-white shadow-lg shadow-primary/25 transition-all hover:translate-y-[-1px] hover:bg-primary/90 hover:shadow-xl hover:shadow-primary/30 active:translate-y-[0px] disabled:translate-y-0 disabled:opacity-70"
                  >
                    <Lock className="mr-2 h-4 w-4 transition-transform group-hover:scale-110" />
                    {state.loading ? 'Authenticating...' : 'Access Console'}
                  </Button>
                </div>
              </form>
            </div>

            <p className="pt-5 text-center text-sm font-medium text-[#5f6e86]">
              Need help?{' '}
              <button className="cursor-pointer text-primary transition-colors hover:text-primary/80">View docs</button> or{' '}
              <button className="cursor-pointer text-primary transition-colors hover:text-primary/80">contact support.</button>
            </p>
          </section>
        </main>
      </div>
    </div>
  )
}
