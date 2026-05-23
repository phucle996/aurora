import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from '@tanstack/react-router'
import {
  Activity,
  ArrowRight,
  Check,
  Copy,
  Eye,
  EyeOff,
  Hourglass,
  Info,
  KeyRound,
  Loader2,
  RefreshCcw,
  ShieldCheck,
} from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { cn } from '@/lib/utils'
import {
  createBootstrapToken,
  fetchTopologyZones,
  waitHypervisorEnrollment,
  type BootstrapTokenResult,
  type HypervisorEnrollmentWaitResult,
  type ZoneOption,
} from '@/lib/hypervisor'

const POLL_INTERVAL_MS = 5_000

const versionOptions = [
  { value: 'latest', label: 'latest' },
  { value: 'stable', label: 'stable' },
]

const phaseLabels: Record<HypervisorEnrollmentWaitResult['phase'], { title: string; detail: string; tone: string }> = {
  token_pending: {
    title: 'Waiting for token consumption',
    detail: 'Install the agent manually and pass the bootstrap token with the selected zone.',
    tone: 'text-blue-600',
  },
  bootstrap_completed: {
    title: 'Certificate issued',
    detail: 'Bootstrap succeeded. The agent is moving to runtime registration.',
    tone: 'text-indigo-600',
  },
  runtime_assignment_pending: {
    title: 'Waiting for runtime assignment',
    detail: 'The agent identity is known, but runtime assignment is not ready yet.',
    tone: 'text-amber-600',
  },
  runtime_registered: {
    title: 'Runtime registered',
    detail: 'The agent is online. Hardware inventory is still being collected.',
    tone: 'text-emerald-600',
  },
  inventory_ready: {
    title: 'Inventory ready',
    detail: 'The node has reported runtime and hardware details.',
    tone: 'text-emerald-600',
  },
  failed: {
    title: 'Enrollment failed',
    detail: 'The enrollment flow reported a terminal failure.',
    tone: 'text-destructive',
  },
  expired: {
    title: 'Bootstrap token expired',
    detail: 'Create a new bootstrap token and retry the manual agent install.',
    tone: 'text-destructive',
  },
}

function CopyTokenButton({ value }: { value: string }) {
  const [copied, setCopied] = useState(false)

  async function handleCopy() {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 2000)
    } catch {
      /* noop */
    }
  }

  return (
    <Button type="button" variant="outline" className="h-12 min-w-[132px] rounded-xl border-slate-200 bg-white px-4 text-sm font-semibold text-slate-900 hover:bg-slate-50" onClick={() => void handleCopy()}>
      {copied ? <Check className="size-4 text-emerald-500" /> : <Copy className="size-4" />}
      {copied ? 'Copied' : 'Copy'}
    </Button>
  )
}

function SectionCard({
  icon,
  title,
  description,
  children,
  className,
}: {
  icon: ReactNode
  title: string
  description: string
  children: React.ReactNode
  className?: string
}) {
  return (
    <section className={cn('rounded-[20px] border border-slate-200 bg-white p-5 text-slate-950 shadow-[0_14px_40px_rgba(15,23,42,0.07)] md:p-6', className)}>
      <div className="flex gap-4">
        <div className="grid h-12 w-12 shrink-0 place-items-center rounded-[18px] border border-blue-100 bg-blue-50 text-blue-600">{icon}</div>
        <div>
          <h3 className="text-[1.1rem] font-bold tracking-[-0.03em] text-slate-950">{title}</h3>
          <p className="mt-1 text-sm leading-6 text-slate-500">{description}</p>
        </div>
      </div>
      <div className="mt-6">{children}</div>
    </section>
  )
}

export function BootstrapKeyForm({
  open,
  onOpenChange,
  onEnrollmentReady,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onEnrollmentReady: () => void
}) {
  const [zones, setZones] = useState<ZoneOption[]>([])
  const [zonesLoading, setZonesLoading] = useState(true)
  const [zoneId, setZoneId] = useState('')
  const [agentVersion, setAgentVersion] = useState('latest')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')
  const [tokenResult, setTokenResult] = useState<BootstrapTokenResult | null>(null)
  const [revealed, setRevealed] = useState(false)
  const [tracking, setTracking] = useState(false)
  const [waitResult, setWaitResult] = useState<HypervisorEnrollmentWaitResult | null>(null)
  const [waitError, setWaitError] = useState('')
  const [pollCount, setPollCount] = useState(0)
  const readyNodeRef = useRef('')

  useEffect(() => {
    if (!open) {
      return
    }
    let active = true
    setZonesLoading(true)
    setError('')
    void fetchTopologyZones()
      .then((items) => {
        if (!active) {
          return
        }
        setZones(items)
        setZoneId((current) => current || items[0]?.id || '')
      })
      .catch((err) => {
        if (active) {
          setError(err instanceof Error ? err.message : 'Cannot load zones')
        }
      })
      .finally(() => {
        if (active) {
          setZonesLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [open])

  useEffect(() => {
    if (!open) {
      setSubmitting(false)
      setTracking(false)
      setWaitResult(null)
      setWaitError('')
      setPollCount(0)
      setTokenResult(null)
      setRevealed(false)
      readyNodeRef.current = ''
    }
  }, [open])

  const selectedZone = useMemo(() => zones.find((item) => item.id === zoneId) ?? null, [zoneId, zones])
  const maskedToken = useMemo(() => {
    if (!tokenResult) {
      return ''
    }
    const value = tokenResult.token.trim()
    if (value.length <= 8) {
      return '•'.repeat(Math.max(8, value.length))
    }
    return `${value.slice(0, 8)} ${'•'.repeat(Math.max(10, value.length - 8))}`
  }, [tokenResult])

  const pollEnrollment = useCallback(async () => {
    if (!tokenResult) {
      return
    }
    try {
      const result = await waitHypervisorEnrollment(tokenResult.bootstrap_token_id, tokenResult.token)
      setWaitResult(result)
      setWaitError('')
      setPollCount((current) => current + 1)

      if (result.ready || result.phase === 'expired' || result.phase === 'failed') {
        setTracking(false)
      }
      if (result.ready && result.node?.id && readyNodeRef.current !== result.node.id) {
        readyNodeRef.current = result.node.id
        onEnrollmentReady()
      }
    } catch (err) {
      setWaitError(err instanceof Error ? err.message : 'Cannot wait for hypervisor enrollment')
      setPollCount((current) => current + 1)
    }
  }, [onEnrollmentReady, tokenResult])

  useEffect(() => {
    if (!tracking || !tokenResult) {
      return
    }
    void pollEnrollment()
    const timer = window.setInterval(() => {
      void pollEnrollment()
    }, POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [pollEnrollment, tokenResult, tracking])

  const submit = useCallback(async () => {
    if (!zoneId) {
      setError('Please select a zone before creating a bootstrap token.')
      return
    }
    setSubmitting(true)
    setError('')
    setWaitError('')
    setWaitResult(null)
    setPollCount(0)
    readyNodeRef.current = ''
    try {
      const created = await createBootstrapToken(agentVersion, zoneId)
      setTokenResult(created)
      setRevealed(false)
      setTracking(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot create bootstrap token')
    } finally {
      setSubmitting(false)
    }
  }, [agentVersion, zoneId])

  const phase = waitResult?.phase ?? 'token_pending'
  const phaseCopy = phaseLabels[phase]
  const node = waitResult?.node

  return (
    <div className="space-y-5 pb-6 text-slate-950">
      <section className="px-1 py-1 md:px-0 md:py-0">
        <div className="space-y-6">
          <div className="flex flex-col gap-4 md:flex-row md:items-start">
            <div className="grid h-14 w-14 shrink-0 place-items-center rounded-[18px] border border-blue-100 bg-gradient-to-br from-blue-50 to-slate-50 shadow-sm">
              <ShieldCheck className="h-7 w-7 text-blue-600" />
            </div>
            <div>
              <h2 className="text-[1.65rem] leading-[1.06] font-bold tracking-[-0.04em] text-slate-950 md:text-[1.9rem]">Add Hypervisor Agent</h2>
              <p className="mt-2 max-w-xl text-[15px] leading-7 text-slate-500">
                Create a bootstrap token, manually install the agent, then track enrollment progress until the agent appears in the list.
              </p>
            </div>
          </div>
          <div className="max-w-xl pl-0 md:pl-[72px]">
            <p className="text-[12px] font-semibold uppercase tracking-[0.18em] text-blue-600">Manual enrollment</p>
            <h3 className="mt-2 text-[1.65rem] leading-[1.08] font-bold tracking-[-0.04em] text-slate-950 md:text-[1.85rem]">Bootstrap Token</h3>
            <p className="mt-3 text-[15px] leading-7 text-slate-500">
              Create a one-time bootstrap token, install the agent manually, then watch enrollment progress here.
            </p>
          </div>
        </div>
      </section>

      <SectionCard
        icon={<KeyRound className="h-6 w-6" />}
        title="Token Configuration"
        description="Choose the zone and version you want to enroll with before creating a one-time bootstrap token."
      >
        <div className="grid gap-4 xl:grid-cols-2">
          <div className="space-y-2.5">
            <Label htmlFor="bootstrap-zone-select" className="mb-2.5 block text-sm font-bold text-slate-900">
              Zone
            </Label>
            {zonesLoading ? (
              <div className="flex h-12 items-center gap-2.5 rounded-xl border border-slate-200 bg-white px-4 text-sm text-slate-500">
                <Spinner className="size-4" /> Loading zones…
              </div>
            ) : zones.length === 0 ? (
              <div className="rounded-2xl border border-red-200 bg-red-50 px-4 py-3 text-sm font-medium text-red-600">
                No zones configured. Create a zone first.
              </div>
            ) : (
              <Select value={zoneId} onValueChange={setZoneId}>
                <SelectTrigger id="bootstrap-zone-select" className="h-12 w-full rounded-xl border-slate-200 bg-white px-4 text-left text-[15px] text-slate-950 shadow-none outline-none focus:border-blue-400 focus:ring-4 focus:ring-blue-100">
                  <SelectValue placeholder="Select a zone…" />
                </SelectTrigger>
                <SelectContent>
                  {zones.map((zone) => (
                    <SelectItem key={zone.id} value={zone.id}>
                      {zone.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>

          <div className="space-y-2.5">
            <Label htmlFor="bootstrap-agent-version" className="mb-2.5 block text-sm font-bold text-slate-900">
              Agent Version
            </Label>
            <Select value={agentVersion} onValueChange={setAgentVersion}>
              <SelectTrigger id="bootstrap-agent-version" className="h-12 w-full rounded-xl border-slate-200 bg-white px-4 text-[15px] text-slate-950 shadow-none outline-none focus:border-blue-400 focus:ring-4 focus:ring-blue-100">
                <SelectValue placeholder="Select a version" />
              </SelectTrigger>
              <SelectContent>
                {versionOptions.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        {error && <div className="mt-5 rounded-2xl border border-red-200 bg-red-50 px-4 py-3 text-sm font-medium text-red-600">{error}</div>}

        <div className="mt-6 flex flex-col-reverse gap-3 sm:flex-row sm:justify-end">
          <Button type="button" variant="outline" className="h-12 rounded-xl border-slate-200 bg-white px-5 text-sm font-semibold text-slate-800 hover:bg-slate-50" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="button" className="h-12 rounded-xl bg-blue-600 px-5 text-sm font-bold text-white shadow-lg shadow-blue-600/25 hover:bg-blue-700" onClick={() => void submit()} disabled={submitting || zonesLoading || zones.length === 0}>
            {submitting ? <Spinner className="size-4" /> : <RefreshCcw className="size-4" />}
            {submitting ? 'Creating…' : 'Create Token'}
          </Button>
        </div>
      </SectionCard>

      {tokenResult && (
        <SectionCard
          icon={<KeyRound className="h-6 w-6" />}
          title="Bootstrap Token"
          description="Use this token with the selected zone when installing the agent manually."
        >
          <div className="flex flex-col gap-3 lg:flex-row">
            <div className="flex h-12 min-w-0 flex-1 items-center rounded-xl border border-slate-200 bg-slate-50 px-4 font-mono text-[15px] text-slate-950"><span className="truncate">{revealed ? tokenResult.token : maskedToken}</span></div>
            <div className="grid grid-cols-2 gap-3 sm:flex sm:flex-none">
              <Button type="button" variant="outline" className="h-12 min-w-[132px] rounded-xl border-slate-200 bg-white px-4 text-sm font-semibold text-slate-900 hover:bg-slate-50" onClick={() => setRevealed((value) => !value)}>
                {revealed ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                {revealed ? 'Hide' : 'Reveal'}
              </Button>
              <CopyTokenButton value={tokenResult.token} />
            </div>
          </div>

          <div className="mt-4 flex gap-3 rounded-xl border border-blue-100 bg-gradient-to-r from-slate-50 to-blue-50 p-4 text-slate-600">
            <div className="flex gap-4">
              <div className="grid h-9 w-9 shrink-0 place-items-center rounded-full bg-blue-600 text-white">
                <Info className="h-5 w-5" />
              </div>
              <p className="text-sm leading-6 md:text-[15px]">
                Install the agent manually with zone <span className="font-bold text-blue-600">{selectedZone?.label || zoneId}</span> and the copied bootstrap token.
              </p>
            </div>
          </div>

          <div className="my-5 h-px bg-slate-200" />

          <div className="flex flex-col gap-4 md:flex-row md:items-center md:justify-between">
            <div className="flex gap-4">
              <div className="grid h-10 w-10 shrink-0 place-items-center rounded-xl border border-blue-100 bg-blue-50 text-blue-600">
                <Activity className="h-5 w-5" />
              </div>
              <div className="space-y-1">
                <h3 className="text-base font-bold text-slate-950">Enrollment Tracking</h3>
                <p className="mt-1 max-w-sm text-sm leading-6 text-slate-500">Start or refresh progress checks against the enrollment wait API.</p>
              </div>
            </div>
            <Button type="button" variant="outline" className="h-12 rounded-xl border-slate-200 bg-white px-4 text-sm font-semibold text-slate-900 hover:bg-slate-50" onClick={() => setTracking((value) => !value)}>
              {tracking ? 'Stop Tracking' : 'Start Tracking'}
            </Button>
          </div>
        </SectionCard>
      )}

      {tokenResult && tracking && (
        <SectionCard
          icon={<Hourglass className="h-6 w-6" />}
          title="Token Status"
          description="Live enrollment progress from the controlplane wait API."
        >
          <div className="flex flex-col gap-5">
            <div className="flex flex-col gap-2.5 sm:flex-row sm:items-start sm:justify-between">
              <div className="space-y-2">
                <div className="flex gap-4">
                  <div className="relative mt-2 h-3 w-3 shrink-0 rounded-full bg-blue-600 shadow-[0_0_0_10px_rgba(37,99,235,0.10)]" />
                  <div><h3 className="text-[1.1rem] font-bold tracking-[-0.03em] text-slate-950">{phaseCopy.title}</h3>
                </div>
                <p className="mt-1 text-sm text-slate-500">{waitResult?.reason || 'waiting for agent bootstrap'}</p></div>
              </div>
              <div className="mt-2 inline-flex items-center gap-2 rounded-full bg-blue-50 px-3 py-2 text-sm font-medium text-slate-500">
                <Loader2 className="size-4 animate-spin text-primary" />
                Poll #{pollCount}
              </div>
            </div>

            {waitError ? (
              <div className="rounded-2xl border border-red-200 bg-red-50 px-4 py-3 text-sm font-medium text-red-600">{waitError}</div>
            ) : (
              <div className="mt-5 flex gap-3 rounded-xl border border-blue-200 bg-blue-50/40 p-4"><div className="grid h-10 w-10 shrink-0 place-items-center rounded-full bg-blue-50 text-blue-600"><Hourglass className="h-6 w-6" /></div><p className="text-sm leading-6 text-slate-700">{phaseCopy.detail}</p></div>
            )}

            {node && (
              <>
                <div className="grid gap-3 rounded-2xl border border-slate-200 bg-slate-50 p-5 md:grid-cols-3">
                  <div>
                    <p className="text-xs font-semibold uppercase tracking-[0.16em] text-muted-foreground">Node</p>
                    <p className="mt-2 text-base font-semibold text-foreground">{node.hostname}</p>
                    <p className="text-xs text-muted-foreground">ID: {node.id}</p>
                  </div>
                  <div>
                    <p className="text-xs font-semibold uppercase tracking-[0.16em] text-muted-foreground">Status</p>
                    <p className="mt-2 text-base font-semibold text-foreground">{node.status}</p>
                  </div>
                  <div>
                    <p className="text-xs font-semibold uppercase tracking-[0.16em] text-muted-foreground">Management IP</p>
                    <p className="mt-2 text-base font-semibold text-foreground">{node.management_ip || 'Pending runtime IP detection'}</p>
                  </div>
                </div>

                <div className="grid gap-3 rounded-2xl border border-slate-200 bg-slate-50 p-5 md:grid-cols-2">
                  <div className="space-y-1.5">
                    <p className="text-xs font-semibold uppercase tracking-[0.16em] text-muted-foreground">Agent</p>
                    <p className="text-base font-semibold text-foreground">{node.agent.agent_id}</p>
                    <p className="text-sm text-muted-foreground">Version: {node.agent.version || 'n/a'}</p>
                    <p className="text-sm text-muted-foreground">Heartbeat: {node.agent.last_heartbeat_at || 'Pending first heartbeat'}</p>
                  </div>
                  <div className="space-y-1.5">
                    <p className="text-xs font-semibold uppercase tracking-[0.16em] text-muted-foreground">Hardware</p>
                    <p className="text-base font-semibold text-foreground">{node.hardware.cpu_cores} cores / {node.hardware.cpu_threads} threads</p>
                    <p className="text-sm text-muted-foreground">{node.hardware.ram_gib} GiB RAM · {node.hardware.ssd_gib} GiB SSD</p>
                    <p className="text-sm text-muted-foreground">{node.hardware.gpu_model || 'No GPU detected'}</p>
                  </div>
                </div>
              </>
            )}

            {waitResult?.ready && node && (
              <div className="flex justify-end">
                <Link to="/hypervisor/$agentId" params={{ agentId: node.id }}>
                  <Button className="h-14 rounded-2xl bg-blue-600 px-6 font-bold text-white shadow-lg shadow-blue-600/25 hover:bg-blue-700">
                    Open Node Details
                    <ArrowRight className="size-4" />
                  </Button>
                </Link>
              </div>
            )}
          </div>
        </SectionCard>
      )}
    </div>
  )
}
