import { useState, useEffect, type FormEvent } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { Send } from 'lucide-react'
import { toast } from 'sonner'

// Import hộp thoại hiển thị tiến trình và kết quả kiểm tra kết nối SMTP
import { TestConnectionDialog } from './sections/TestConnectionDialog'
// Import component con quản lý các trường nhập liệu của form (Basic Config & Security Certs)
import { EndpointFormFields, type EndpointForm } from './sections/EndpointFormFields'
// Import component con hiển thị giao diện xem trước (Live Preview) thời gian thực ở cột bên phải
import { EndpointPreviewCard } from './sections/EndpointPreviewCard'
import { Button } from '@/components/ui/button'
import { Fetch } from '@/lib/fetch'
import { usePageMeta } from '@/lib/page-meta'
import { PageContent } from '@/components/layout/layout'

/**
 * Khai báo giá trị khởi tạo mặc định cho biểu mẫu cấu hình SMTP Endpoint.
 * Theo yêu cầu thiết kế hệ thống HA & Cloud Native, khi tạo mới một endpoint:
 * - Trạng thái hoạt động (status) mặc định ban đầu luôn là 'planned' (lập kế hoạch).
 * - Các chứng chỉ bảo mật và thông tin xác thực để trống.
 * - Cổng SMTP mặc định là 587 (chuẩn STARTTLS).
 * - Số lượng kết nối song song tối đa (max_connections) mặc định là 10.
 */
const initialForm: EndpointForm = {
  zone_id: '',
  name: '',
  host: '',
  port: 587,
  username: '',
  priority: 100,
  weight: 1,
  warmup_state: 'stable',
  status: 'planned', // Trạng thái mặc định là 'planned' giống như Zone khi tạo mới
  tls_mode: 'starttls',
  password: '',
  ca_cert_pem: '',
  client_cert_pem: '',
  client_key_pem: '',
  max_connections: 10,
}

// Định nghĩa cấu trúc phản hồi lỗi/thành công chuẩn hóa từ API Control Plane
type APIResponse<T = unknown> = {
  data?: T
  message?: string
  error?: string
}

/**
 * Component Page chính cho việc tạo mới Endpoint gửi thư (NewMailEndpointPage).
 * Tổ chức giao diện dạng Dashboard 2 cột chuẩn SRE:
 * - Cột trái: Form nhập các thông tin cấu hình chi tiết (Basic + Security).
 * - Cột phải: Panel Live Preview cập nhật động dữ liệu realtime khi người dùng gõ.
 * - Hỗ trợ tính năng "Try Connect" kích hoạt bắt tay SMTP trực tiếp từ trình duyệt thông qua Gateway.
 */
export default function NewMailEndpointPage() {
  // Cập nhật thẻ tiêu đề <title> và meta description động cho trình duyệt để tối ưu SEO
  usePageMeta('New Mail Endpoint | Aurora Admin', 'Create a new Mail endpoint for outbound email routing.')
  const navigate = useNavigate()

  // State quản lý toàn bộ dữ liệu biểu mẫu SMTP Endpoint
  const [form, setForm] = useState<EndpointForm>(initialForm)
  // State quản lý danh sách các Zone lấy từ API
  const [zones, setZones] = useState<{ id: string; name: string }[]>([])
  // State quản lý trạng thái submit form lên server (để hiển thị loading/disable nút bấm)
  const [loading, setLoading] = useState(false)
  // State lưu trữ và hiển thị thông điệp lỗi hệ thống/lỗi API
  const [error, setError] = useState('')

  // State quản lý trạng thái hộp thoại kiểm tra kết nối SMTP (Try Connect)
  const [testState, setTestState] = useState<{
    isOpen: boolean
    loading: boolean
    success: boolean | null
    message: string
  }>({
    isOpen: false,
    loading: false,
    success: null,
    message: '',
  })

  // Gọi API lấy danh sách các Zone khi component được mount
  useEffect(() => {
    let active = true
    async function loadZones() {
      try {
        const resp = await Fetch('/admin/core/zones')
        if (resp.ok) {
          const body = await resp.json()
          if (active && body.data?.items) {
            const list = body.data.items.map((z: any) => ({ id: z.id, name: z.name }))
            setZones(list)
            if (list.length > 0) {
              setForm(prev => ({ ...prev, zone_id: prev.zone_id || list[0].id }))
            }
          }
        }
      } catch (err) {
        console.error('Cannot load zones', err)
      }
    }
    void loadZones()
    return () => { active = false }
  }, [])

  /**
   * Cập nhật động giá trị của một trường dữ liệu trong form state.
   * Tự động kiểm tra nếu khóa thuộc nhóm numericKeys thì thực hiện chuyển đổi kiểu dữ liệu
   * sang dạng Number (Float/Int) trước khi lưu vào state để đảm bảo tính nhất quán của dữ liệu gửi lên API.
   */
  const update = (key: keyof EndpointForm, value: string) => {
    setForm((current) => ({ ...current, [key]: numericKeys.has(key) ? Number(value) : value }))
  }

  /**
   * Xử lý sự kiện gửi biểu mẫu (submit form) để tạo mới Endpoint.
   * Quy trình xử lý:
   * 1. Ngăn chặn hành vi submit mặc định của trình duyệt.
   * 2. Gọi hàm validation client-side để kiểm tra tính hợp lệ của dữ liệu (tránh lỗi logic).
   * 3. Chuyển đổi form state sang payload chuẩn hóa (endpointPayload).
   * 4. Gọi API POST `/admin/mail/endpoints` thông qua client Fetch tích hợp sẵn CSRF token.
   * 5. Hiển thị toast thông báo thành công và chuyển hướng về danh sách quản lý.
   */
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    
    // Validate dữ liệu client-side trước khi gửi yêu cầu mạng
    const validationError = getEndpointFormValidationError(form)
    if (validationError) {
      setError(validationError)
      toast.error(validationError)
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
      
      toast.success('Mail endpoint created successfully!')
      // Điều hướng về trang quản lý email, tự động scroll đến tab endpoints
      await navigate({ to: '/mail', hash: 'endpoints' })
    } catch (err) {
      const errMsg = err instanceof Error ? err.message : 'Cannot create endpoint.'
      setError(errMsg)
      toast.error(errMsg)
    } finally {
      setLoading(false)
    }
  }

  /**
   * Kích hoạt tiến trình kiểm tra kết nối (Try Connect) SMTP tạm thời đến Host/Port chỉ định.
   * Giúp quản trị viên xác thực thông tin tài khoản, máy chủ và cơ chế bảo mật TLS/mTLS 
   * hoạt động đúng đắn trước khi lưu cấu hình chính thức vào cơ sở dữ liệu.
   */
  const tryConnect = async () => {
    // Validate thông tin form trước khi gửi request test kết nối
    const validationError = getEndpointFormValidationError(form)
    if (validationError) {
      setError(validationError)
      toast.error(validationError)
      return
    }

    // Mở modal thông báo tiến trình kết nối thử
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

      // Cập nhật kết quả phản hồi của API lên modal
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

  // Loại bỏ khoảng trắng thừa ở các trường quan trọng để kiểm tra điều kiện submit
  const trimmedName = form.name.trim()
  const trimmedHost = form.host.trim()
  // Nút submit chỉ hoạt động khi Tên, Host và Zone đã được chọn/nhập và không trong trạng thái đang gửi request
  const canSubmit = trimmedName !== '' && trimmedHost !== '' && form.zone_id.trim() !== '' && !loading

  return (
    <PageContent className="pb-0">
      {/* 1. Page Header: Gồm Breadcrumb điều hướng và các nút hành động chính */}
      <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div className="space-y-4">
          <nav className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
            <Link to="/mail" hash="endpoints" className="text-primary hover:underline">
              Mail
            </Link>
            <span>/</span>
            <span>Add SMTP-Compatible Endpoint</span>
          </nav>
          <div className="space-y-2">
            <h1 className="text-3xl font-semibold tracking-[-0.03em] text-foreground md:text-4xl">
              Add SMTP-Compatible Endpoint
            </h1>
            <p className="text-sm text-muted-foreground md:text-base">
              Create an admin-managed SMTP-compatible mail endpoint for delivery routing.
            </p>
          </div>
        </div>

        {/* Cụm Action Buttons trên Header */}
        <div className="flex items-center gap-3 lg:pt-10">
          {/* Nút kiểm tra kết nối SMTP tạm thời */}
          <Button type="button" variant="outline" className="h-12 rounded-lg px-8 text-sm font-semibold cursor-pointer" onClick={() => void tryConnect()} disabled={loading}>
            <Send className="size-4 mr-2" />
            Try Connect
          </Button>
          {/* Nút hủy bỏ, quay về trang danh sách */}
          <Button asChild variant="outline" className="h-12 rounded-lg px-8 text-sm font-semibold">
            <Link to="/mail" hash="endpoints">Cancel</Link>
          </Button>
          {/* Nút kích hoạt submit form tạo mới chính thức */}
          <Button
            type="submit"
            onClick={() => {
              const f = document.getElementById('new-endpoint-form') as HTMLFormElement | null
              if (f) f.requestSubmit()
            }}
            className="h-12 rounded-lg px-8 text-sm font-semibold shadow-sm cursor-pointer"
            disabled={!canSubmit}
          >
            {loading ? 'Creating...' : 'Create Endpoint'}
          </Button>
        </div>
      </div>

      {/* 2. Alert Box: Hiển thị lỗi phát sinh trong quá trình xử lý nếu có */}
      {error && (
        <div className="rounded-xl border border-destructive/20 bg-destructive/10 px-4 py-3 text-sm font-medium text-destructive">
          {error}
        </div>
      )}

      {/* 3. Main Grid Layout: Phân bổ Form cấu hình và Live Preview Card */}
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1fr)_400px]">
        {/* Form cấu hình bên trái */}
        <form id="new-endpoint-form" className="space-y-6" onSubmit={(event) => void submit(event)}>
          <EndpointFormFields form={form} update={update} zones={zones} />
        </form>

        {/* Live Preview hiển thị bên phải, bám dính (sticky) khi cuộn chuột */}
        <EndpointPreviewCard
          name={form.name}
          host={form.host}
          port={form.port}
          tlsMode={form.tls_mode}
          priority={form.priority}
          weight={form.weight}
          maxConnections={form.max_connections}
          username={form.username}
        />
      </div>

      {/* 4. Modal Test Connection Dialog: Hiển thị logs/kết quả bắt tay SMTP */}
      <TestConnectionDialog
        isOpen={testState.isOpen}
        onOpenChange={(open) => setTestState(prev => ({ ...prev, isOpen: open }))}
        loading={testState.loading}
        success={testState.success}
        message={testState.message}
        endpointName={form.name || 'New Endpoint'}
      />
    </PageContent>
  )
}

// Tập hợp các trường dữ liệu cần được parse sang dạng Số trước khi chuyển đổi payload gửi API
const numericKeys = new Set<keyof EndpointForm>(['port', 'priority', 'weight', 'max_connections'])

/**
 * Chuẩn hóa cấu trúc dữ liệu form (FormState) thành Payload DTO gửi lên API.
 * Chuyển đổi các chuỗi chứng chỉ PEM trống thành `undefined` thay vì gửi chuỗi rỗng
 * để hệ thống Backend Go nhận diện chính xác giá trị rỗng/không thiết lập.
 */
function endpointPayload(form: EndpointForm) {
  return {
    ...form,
    ca_cert_pem: form.ca_cert_pem || undefined,
    client_cert_pem: form.client_cert_pem || undefined,
    client_key_pem: form.client_key_pem || undefined,
  }
}

/**
 * Hàm kiểm tra tính hợp lý của dữ liệu biểu mẫu trên Client-side.
 * Giúp phát hiện sớm các cấu hình lỗi trước khi gửi request mạng:
 * - Ngăn chặn giá trị số lượng kết nối tối đa (Max Connections) bị âm.
 * - Yêu cầu đầy đủ chứng chỉ client PEM và private key PEM khi chọn chế độ mTLS.
 */
function getEndpointFormValidationError(form: EndpointForm): string | null {
  if (form.zone_id.trim() === '') {
    return 'Please select an infrastructure zone.'
  }
  if (form.max_connections < 0) {
    return 'Max Connections cannot be negative.'
  }
  if (form.tls_mode === 'mtls' && form.client_cert_pem.trim() === '') {
    return 'mTLS endpoints require a client certificate PEM.'
  }
  if (form.tls_mode === 'mtls' && form.client_key_pem.trim() === '') {
    return 'mTLS endpoints require a client private key PEM.'
  }
  return null
}

/**
 * Đọc và phân giải chi tiết thông báo lỗi trả về từ API Control Plane.
 * Ưu tiên trích xuất trường `message` hoặc `error` tùy thuộc vào định dạng JSON
 * của response lỗi từ Backend Go.
 */
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
