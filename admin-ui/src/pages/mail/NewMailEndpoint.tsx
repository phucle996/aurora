import { useState, type FormEvent, type ReactNode } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import {
  BadgeInfo,
  ChevronLeft,
  KeyRound,
  LockKeyhole,
  Scale,
  Send,
  Server,
  ShieldCheck,
  Star,
  Tags,
  User,
  Vault,
  WandSparkles,
  Zap,
} from 'lucide-react'

// Dialog kiểm tra khả năng kết nối thử SMTP trước khi tạo thực tế
import { TestConnectionDialog } from './sections/TestConnectionDialog'

import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { Fetch } from '@/lib/fetch'
import { usePageMeta } from '@/lib/page-meta'

// Khai báo giá trị khởi tạo ban đầu cho form cấu hình Endpoint SMTP
const initialForm = {
  name: '',
  host: '',
  port: 587,
  username: '',
  priority: 100,
  weight: 1,
  warmup_state: 'stable',
  status: 'disabled',
  tls_mode: 'starttls',
  password: '',
  ca_cert_pem: '',
  client_cert_pem: '',
  client_key_pem: '',
  max_connections: 10,
  secret_ref: '',
}

type EndpointForm = typeof initialForm

// Cấu hình các tùy chọn phương thức mã hóa SSL/TLS
const tlsModeOptions = [
  { value: 'none', label: 'None' },
  { value: 'starttls', label: 'STARTTLS' },
  { value: 'tls', label: 'TLS' },
  { value: 'mtls', label: 'mTLS' },
]

// Cấu hình các tùy chọn trạng thái hoạt động của Endpoint
const statusOptions = [
  { value: 'disabled', label: 'Disabled' },
  { value: 'active', label: 'Active' },
  { value: 'suspended', label: 'Suspended' },
]

// Cấu hình các tùy chọn chế độ khởi động uy tín của địa chỉ IP (warmup)
const warmupOptions = [
  { value: 'stable', label: 'Stable' },
  { value: 'warming', label: 'Warming' },
  { value: 'paused', label: 'Paused' },
]

type APIResponse<T = unknown> = {
  data?: T
  message?: string
  error?: string
}

/**
 * Component tạo mới một Endpoint gửi thư SMTP (NewMailEndpointPage)
 * Cung cấp biểu mẫu cấu hình cao cho phép kết nối thử nghiệm realtime, nhập chứng chỉ bảo mật và các cài đặt lưu lượng nâng cao.
 */
export default function NewMailEndpointPage() {
  // Đặt tiêu đề meta động cho trang web
  usePageMeta('New Mail Endpoint | Aurora Admin', 'Create a new Mail endpoint for outbound email routing.')
  const navigate = useNavigate()
  
  // State quản lý toàn bộ dữ liệu của form nhập liệu
  const [form, setForm] = useState<EndpointForm>(initialForm)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  // State quản lý trạng thái hiển thị và kết quả kiểm tra kết nối SMTP
  const [testState, setTestState] = useState<{
    isOpen: boolean;
    loading: boolean;
    success: boolean | null;
    message: string;
  }>({
    isOpen: false,
    loading: false,
    success: null,
    message: '',
  })

  // Hàm cập nhật giá trị form, tự động chuyển đổi sang dạng số đối với các trường numeric
  const update = (key: keyof EndpointForm, value: string) => {
    setForm((current) => ({ ...current, [key]: numericKeys.has(key) ? Number(value) : value }))
  }

  // Thao tác gửi biểu mẫu tạo mới Endpoint SMTP
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    // Kiểm tra tính hợp lệ dữ liệu biểu mẫu ở client-side trước khi gửi
    const validationError = getEndpointFormValidationError(form)
    if (validationError) {
      setError(validationError)
      return
    }
    setLoading(true)
    setError('')
    try {
      const resp = await Fetch('/admin/mail/endpoints', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(endpointPayload(form)),
      })
      if (!resp.ok) throw new Error(await readAPIMessage(resp, 'Cannot create endpoint.'))
      // Điều hướng về màn hình quản trị Mail Admin sau khi tạo thành công
      await navigate({ to: '/mail', hash: 'endpoints' })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot create endpoint.')
    } finally {
      setLoading(false)
    }
  }

  // Thao tác gửi lệnh kết nối thử tới SMTP Server trực tiếp
  const tryConnect = async () => {
    const validationError = getEndpointFormValidationError(form)
    if (validationError) {
      setError(validationError)
      return
    }
    setTestState({
      isOpen: true,
      loading: true,
      success: null,
      message: 'Testing connection...',
    })
    setError('')
    try {
      const resp = await Fetch('/admin/mail/endpoints/try-connect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(endpointPayload(form)),
      })
      const resultMessage = await readAPIMessage(
        resp,
        resp.ok ? 'Connection successful' : 'Connection failed',
      )

      setTestState(prev => ({
        ...prev,
        loading: false,
        success: resp.ok,
        message: resultMessage || (resp.ok ? 'Connection successful' : 'Connection failed'),
      }))
    } catch (err) {
      setTestState(prev => ({
        ...prev,
        loading: false,
        success: false,
        message: err instanceof Error ? err.message : 'An unexpected error occurred.',
      }))
    }
  }

  return (
    <TooltipProvider>
      <form className="space-y-5 pb-12" onSubmit={(event) => void submit(event)}>
      {/* Tiêu đề trang và Nút điều khiển lưu / thử nghiệm */}
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-2">
          <div className="flex items-center gap-3">
            {/* Nút quay lại trang danh sách chính */}
            <Button asChild variant="outline" size="icon" className="size-10 rounded-xl border-border/80 bg-card shadow-sm">
              <Link to="/mail" hash="endpoints" aria-label="Back to Mail endpoints"><ChevronLeft className="size-5" /></Link>
            </Button>
            <h1 className="text-3xl font-semibold tracking-[-0.03em] text-foreground">Add Mail Endpoint</h1>
            <span className="flex size-10 items-center justify-center rounded-xl bg-primary/10 text-primary shadow-sm">
              <Send className="size-5" />
            </span>
          </div>
          <p className="ml-13 text-sm text-muted-foreground">Create an admin-managed SMTP endpoint for delivery routing.</p>
        </div>
        <div className="flex gap-3">
          {/* Nút kiểm tra thử kết nối SMTP */}
          <Button type="button" variant="outline" className="h-10 rounded-xl px-4 font-medium shadow-sm" onClick={() => void tryConnect()} disabled={loading}>
            <Send className="size-4" />
            Try Connect
          </Button>
          {/* Nút lưu chính thức */}
          <Button type="submit" className="h-10 rounded-xl bg-linear-to-r from-[#3b82f6] to-[#5b5df7] px-4 font-medium shadow-[0_14px_34px_rgba(59,130,246,0.28)]" disabled={loading}>
            <WandSparkles className="size-4" />
            {loading ? 'Saving...' : 'Create Endpoint'}
          </Button>
        </div>
      </div>

      {/* Hiển thị lỗi tổng hợp */}
      {error && <div className="rounded-xl border border-destructive/20 bg-destructive/10 p-3 text-sm font-semibold text-destructive">{error}</div>}

      {/* 1. Phần cấu hình thông tin kết nối và xác thực cơ bản */}
      <SectionCard icon={<Server className="size-4" />} title="Basic Configuration">
        <div className="grid gap-6 lg:grid-cols-3">
          <Field label="Name" required><IconInput icon={<Tags className="size-4" />} value={form.name} onChange={(value) => update('name', value)} placeholder="Enter endpoint name" required /></Field>
          <Field label="Host" required><IconInput icon={<Server className="size-4" />} value={form.host} onChange={(value) => update('host', value)} placeholder="mail.example.com" required /></Field>
          <Field label="Port" required><IconInput icon={<Vault className="size-4" />} type="number" value={form.port} onChange={(value) => update('port', value)} required /></Field>
          <Field label="Username" required><IconInput icon={<User className="size-4" />} value={form.username} onChange={(value) => update('username', value)} placeholder="Enter username" /></Field>
          <Field label="Password"><IconInput icon={<LockKeyhole className="size-4" />} type="password" value={form.password} onChange={(value) => update('password', value)} placeholder="Enter password" /></Field>
          <Field label="Secret Reference" hint="Optional metadata reference stored with this endpoint. Aurora Mail still requires the direct password for authenticated Mail connections.">
            <>
              <IconInput icon={<KeyRound className="size-4" />} value={form.secret_ref} onChange={(value) => update('secret_ref', value)} placeholder="External secret ID" />
              {form.secret_ref.trim() !== '' && form.username.trim() !== '' && form.password.trim() === '' ? (
                <p className="text-xs font-medium text-amber-600">Secret references are stored for future integrations only. Enter the Mail password directly to authenticate this endpoint.</p>
              ) : null}
            </>
          </Field>
          <Field label="TLS Mode" required><IconSelect icon={<ShieldCheck className="size-4" />} value={form.tls_mode} onValueChange={(value) => update('tls_mode', value)} options={tlsModeOptions} /></Field>
          <Field label="Status" required><IconSelect icon={<span className="size-3 rounded-full bg-emerald-500" />} value={form.status} onValueChange={(value) => update('status', value)} options={statusOptions} /></Field>
          <Field label="Warmup State" hint="Endpoints in 'warming' mode may have different rate limits as they age to build reputation."><IconSelect icon={<Zap className="size-4 text-orange-500" />} value={form.warmup_state} onValueChange={(value) => update('warmup_state', value)} options={warmupOptions} /></Field>
          <Field label="Priority" hint="Lower values have higher priority. The system will use highest priority endpoints first."><IconInput icon={<Star className="size-4" />} type="number" value={form.priority} onChange={(value) => update('priority', value)} /></Field>
          <Field label="Weight" hint="Relative weight for load balancing. Higher weight means this endpoint handles more traffic."><IconInput icon={<Scale className="size-4" />} type="number" value={form.weight} onChange={(value) => update('weight', value)} /></Field>
          <Field label="Max Connections" hint="The maximum number of concurrent connections allowed to this SMTP server."><IconInput icon={<Zap className="size-4" />} type="number" value={form.max_connections} onChange={(value) => update('max_connections', value)} /></Field>
        </div>
      </SectionCard>

      {/* 2. Phần cấu hình chứng chỉ bảo mật (chỉ hiển thị khi TLS khác None) */}
      {form.tls_mode !== 'none' && (
        <SectionCard icon={<LockKeyhole className="size-4" />} title="Security & Certificates" description="Configure SSL/TLS certificates for secure communication.">
          <div className="grid gap-6 lg:grid-cols-2">
            <Field label="CA Cert PEM"><IconTextarea icon={<BadgeInfo className="size-4" />} value={form.ca_cert_pem} onChange={(value) => update('ca_cert_pem', value)} placeholder={'-----BEGIN CERTIFICATE-----\n...'} /></Field>
            {form.tls_mode === 'mtls' && (
              <>
                <Field label="Client Cert PEM"><IconTextarea icon={<BadgeInfo className="size-4" />} value={form.client_cert_pem} onChange={(value) => update('client_cert_pem', value)} placeholder={'-----BEGIN CERTIFICATE-----\n...'} /></Field>
                <Field label="Client Key PEM"><IconTextarea icon={<KeyRound className="size-4" />} value={form.client_key_pem} onChange={(value) => update('client_key_pem', value)} placeholder={'-----BEGIN PRIVATE KEY-----\n...'} /></Field>
              </>
            )}
          </div>
        </SectionCard>
      )}

      {/* Dialog overlay hiển thị kết quả kiểm tra kết nối thử SMTP */}
      <TestConnectionDialog 
        isOpen={testState.isOpen}
        onOpenChange={(open) => setTestState(prev => ({ ...prev, isOpen: open }))}
        loading={testState.loading}
        success={testState.success}
        message={testState.message}
        endpointName={form.name || 'New Endpoint'}
      />
    </form>
    </TooltipProvider>
  )
}

// Bố cục khung Card bao bọc từng nhóm cài đặt cấu hình
function SectionCard({ children, description, icon, title }: { children: ReactNode; description?: string; icon: ReactNode; title: string }) {
  return (
    <section className="rounded-2xl border border-border/80 bg-card p-7 shadow-[0_18px_70px_rgba(15,23,42,0.06)] backdrop-blur-xl">
      <div className="mb-6 flex items-start gap-3">
        <span className="mt-0.5 text-primary">{icon}</span>
        <div>
          <h2 className="text-sm font-semibold uppercase tracking-wide text-foreground">{title}</h2>
          {description ? <p className="mt-1 text-sm text-muted-foreground">{description}</p> : null}
        </div>
      </div>
      {children}
    </section>
  )
}

// Component trường nhập liệu tiêu chuẩn có nhãn và tooltip gợi ý
function Field({ children, hint, label, required }: { children: ReactNode; hint?: string; label: string; required?: boolean }) {
  return (
    <label className="space-y-2 text-sm font-semibold text-foreground block">
      <span className="flex items-center gap-1.5">
        {label}
        {required ? <span className="text-destructive">*</span> : null}
        {hint ? (
          <Tooltip>
            <TooltipTrigger asChild>
              <BadgeInfo className="size-3.5 cursor-help text-muted-foreground inline" />
            </TooltipTrigger>
            <TooltipContent side="top" className="max-w-50 text-xs font-normal">
              {hint}
            </TooltipContent>
          </Tooltip>
        ) : null}
      </span>
      {children}
    </label>
  )
}

// Input có tích hợp sẵn Icon sang trọng ở lề trái
function IconInput({ icon, onChange, value, ...props }: { icon: ReactNode; onChange: (value: string) => void; value: number | string } & Omit<React.ComponentProps<typeof Input>, 'onChange' | 'value'>) {
  return (
    <div className="relative">
      <span className="absolute inset-y-0 left-0 flex w-11 items-center justify-center rounded-l-xl border-r border-border/70 bg-muted/35 text-muted-foreground">{icon}</span>
      <Input className="h-12 rounded-xl border-border/80 bg-background/70 pl-14 text-sm font-medium shadow-sm" value={value} onChange={(event) => onChange(event.target.value)} {...props} />
    </div>
  )
}

// Select có tích hợp Icon sang trọng ở lề trái
function IconSelect({ icon, onValueChange, options, value }: { icon: ReactNode; onValueChange: (value: string) => void; options: Array<{ label: string; value: string }>; value: string }) {
  return (
    <div className="relative">
      <span className="absolute inset-y-0 left-0 z-10 flex w-11 items-center justify-center rounded-l-xl border-r border-border/70 bg-muted/35 text-muted-foreground">{icon}</span>
      <Select value={value} onValueChange={onValueChange}>
        <SelectTrigger className="h-12 w-full rounded-xl border-border/80 bg-background/70 pl-14 text-sm font-medium shadow-sm"><SelectValue /></SelectTrigger>
        <SelectContent>{options.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent>
      </Select>
    </div>
  )
}

// Textarea nhập văn bản lớn có tích hợp Icon ở lề trái
function IconTextarea({ icon, onChange, value, ...props }: { icon: ReactNode; onChange: (value: string) => void; value: string } & Omit<React.ComponentProps<typeof Textarea>, 'onChange' | 'value'>) {
  return (
    <div className="relative">
      <span className="absolute bottom-0 left-0 top-0 flex w-11 items-start justify-center rounded-l-xl border-r border-border/70 bg-muted/35 pt-4 text-muted-foreground">{icon}</span>
      <Textarea className="min-h-20 rounded-xl border-border/80 bg-background/70 pl-14 font-mono text-sm shadow-sm" value={value} onChange={(event) => onChange(event.target.value)} {...props} />
    </div>
  )
}

// Mảng chứa danh sách các keys cần được tự động parse số
const numericKeys = new Set<keyof EndpointForm>(['port', 'priority', 'weight', 'max_connections'])

// Chuẩn bị payload gửi lên API loại bỏ các chuỗi trống để API nhận diện chính xác
function endpointPayload(form: EndpointForm) {
  return {
    ...form,
    ca_cert_pem: form.ca_cert_pem || undefined,
    client_cert_pem: form.client_cert_pem || undefined,
    client_key_pem: form.client_key_pem || undefined,
    secret_ref: form.secret_ref || undefined,
  }
}

// Hàm kiểm tra lỗi logic đầu vào biểu mẫu ở client-side
function getEndpointFormValidationError(form: EndpointForm): string | null {
  if (form.secret_ref.trim() !== '' && form.username.trim() !== '' && form.password.trim() === '') {
    return 'Secret references cannot replace the SMTP password yet. Enter the password directly for authenticated endpoints.'
  }
  if (form.tls_mode === 'mtls' && form.client_cert_pem.trim() === '') {
    return 'mTLS endpoints require a client certificate PEM.'
  }
  if (form.tls_mode === 'mtls' && form.client_key_pem.trim() === '') {
    return 'mTLS endpoints require a client private key PEM.'
  }
  return null
}

// Đọc thông điệp lỗi hoặc thông báo thành công trả về từ API phản hồi
async function readAPIMessage(resp: Response, fallback: string): Promise<string> {
  const body = (await resp.json().catch(() => null)) as APIResponse | null
  const message = body?.message?.trim()
  const error = body?.error?.trim()

  if (message && message.toLowerCase() !== 'internal server error' && message.toLowerCase() !== 'service unavailable') {
    return message
  }
  if (error) {
    return error
  }
  if (message) {
    return message
  }
  return fallback
}
