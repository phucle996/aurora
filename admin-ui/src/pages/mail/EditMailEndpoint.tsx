import { useEffect, useState, type ReactNode } from 'react'
import { useNavigate, useParams } from '@tanstack/react-router'
import { ChevronLeft, Key, Loader2, Save, Server, ShieldCheck } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Fetch } from '@/lib/fetch'
import { usePageMeta } from '@/lib/page-meta'

// Định nghĩa cấu trúc của biểu mẫu chỉnh sửa Endpoint SMTP
type EndpointForm = {
  name: string; host: string; port: number; username: string; password: string
  tls_mode: string; max_connections: number; priority: number; weight: number
  status: string; ca_cert_pem: string; client_cert_pem: string; client_key_pem: string
}

// Đọc thông điệp lỗi hoặc phản hồi trả về từ API
async function readMsg(resp: Response, fallback: string): Promise<string> {
  try { 
    const b = await resp.json()
    return b?.message ?? b?.error ?? fallback 
  } catch { 
    return fallback 
  }
}

/**
 * Component cập nhật / chỉnh sửa thông tin của Endpoint SMTP hiện tại (EditMailEndpointPage)
 * Tự động tải thông tin hiện tại từ API bằng mã ID nhận từ route params, hỗ trợ cập nhật động các trường số và chứng chỉ SSL/TLS.
 */
export default function EditMailEndpointPage() {
  // Cập nhật thẻ metadata tiêu đề động cho trình duyệt
  usePageMeta('Edit Mail Endpoint | Aurora Admin', 'Update Mail endpoint settings and connectivity options.')
  const navigate = useNavigate()
  
  // Đọc mã ID của Endpoint cần chỉnh sửa trực tiếp từ URL route params
  const { id } = useParams({ strict: false }) as { id: string }
  
  // States quản lý trạng thái tải thông tin, lưu dữ liệu, báo lỗi và lưu form
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [form, setForm] = useState<EndpointForm>({
    name: '', host: '', port: 587, username: '', password: '',
    tls_mode: 'starttls', max_connections: 16, priority: 100, weight: 1,
    status: 'active', ca_cert_pem: '', client_cert_pem: '', client_key_pem: '',
  })

  // Gọi API tải thông tin hiện tại của Endpoint dựa trên ID khi component mount
  useEffect(() => {
    async function load() {
      try {
        const resp = await Fetch(`/admin/mail/endpoints/${id}`)
        if (!resp.ok) { setError(await readMsg(resp, 'Failed to load')); return }
        const body = await resp.json()
        const ep = body.data ?? body
        
        // Cập nhật lại form với các dữ liệu hiện có trong cơ sở dữ liệu
        setForm({
          name: ep.name ?? '', host: ep.host ?? '', port: ep.port ?? 587,
          username: ep.username ?? '', password: '', tls_mode: ep.tls_mode ?? 'starttls',
          max_connections: ep.max_connections ?? 16, priority: ep.priority ?? 100,
          weight: ep.weight ?? 1, status: ep.status ?? 'active',
          ca_cert_pem: '', client_cert_pem: '', client_key_pem: '',
        })
      } catch { 
        setError('Failed to load endpoint') 
      } finally { 
        setLoading(false) 
      }
    }
    void load()
  }, [id])

  // Cập nhật nhanh một trường dữ liệu trong form
  const set = (key: string, value: unknown) => setForm(prev => ({ ...prev, [key]: value }))

  // Xử lý gửi biểu mẫu cập nhật thông tin Endpoint lên máy chủ
  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setSaving(true)
    setError('')
    try {
      const payload = {
        ...form,
        ca_cert_pem: form.ca_cert_pem || undefined,
        client_cert_pem: form.client_cert_pem || undefined,
        client_key_pem: form.client_key_pem || undefined,
      }
      const resp = await Fetch(`/admin/mail/endpoints/${id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      })
      if (!resp.ok) { setError(await readMsg(resp, 'Update failed')); setSaving(false); return }
      // Trở lại trang Mail Admin ở tab Endpoints sau khi lưu thành công
      navigate({ to: '/mail', hash: 'endpoints' })
    } catch { 
      setError('Update failed') 
    } finally { 
      setSaving(false) 
    }
  }

  // Hiển thị vòng xoay Loading khi đang tải dữ liệu ban đầu từ API
  if (loading) return <div className="flex items-center justify-center h-64"><Loader2 className="size-8 animate-spin text-muted-foreground" /></div>

  return (
    <>
      <div className="mx-auto max-w-3xl space-y-8 p-6">
        {/* Header chỉnh sửa với nút quay lại nhanh */}
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="icon" onClick={() => navigate({ to: '/mail', hash: 'endpoints' })}>
            <ChevronLeft className="size-5" />
          </Button>
          <div>
            <h1 className="text-2xl font-bold">Edit Endpoint</h1>
            <p className="text-xs font-mono text-muted-foreground">{id}</p>
          </div>
        </div>
        
        {/* Vùng hiển thị thông điệp báo lỗi từ API */}
        {error && <div className="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/20 dark:text-red-400">{error}</div>}
        
        <form onSubmit={handleSubmit} className="space-y-6">
          {/* Section 1: Cấu hình thông tin kết nối máy chủ cơ bản */}
          <Section icon={<Server className="size-4" />} title="Connection">
            <div className="grid gap-4 md:grid-cols-2">
              <div><Label>Name</Label><Input value={form.name} onChange={e => set('name', e.target.value)} required /></div>
              <div><Label>Host</Label><Input value={form.host} onChange={e => set('host', e.target.value)} required /></div>
              <div><Label>Port</Label><Input type="number" value={form.port} onChange={e => set('port', parseInt(e.target.value) || 587)} /></div>
              <div>
                <Label>TLS Mode</Label>
                <Select value={form.tls_mode} onValueChange={v => set('tls_mode', v)}>
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">None</SelectItem>
                    <SelectItem value="starttls">STARTTLS</SelectItem>
                    <SelectItem value="tls">TLS</SelectItem>
                    <SelectItem value="mtls">mTLS</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
          </Section>
          
          {/* Section 2: Thông tin xác thực tài khoản và các chứng chỉ mã hóa */}
          <Section icon={<Key className="size-4" />} title="Credentials">
            <div className="grid gap-4 md:grid-cols-2">
              <div><Label>Username</Label><Input value={form.username} onChange={e => set('username', e.target.value)} /></div>
              <div><Label>Password</Label><Input type="password" value={form.password} onChange={e => set('password', e.target.value)} placeholder="Leave empty to keep current" /></div>
            </div>
            {(form.tls_mode === 'tls' || form.tls_mode === 'mtls') && (
              <div>
                <Label>CA Certificate (PEM)</Label>
                <Textarea value={form.ca_cert_pem} onChange={e => set('ca_cert_pem', e.target.value)} rows={3} className="font-mono text-xs" placeholder="Leave empty to keep current" />
              </div>
            )}
            {form.tls_mode === 'mtls' && (
              <>
                <div><Label>Client Certificate (PEM)</Label><Textarea value={form.client_cert_pem} onChange={e => set('client_cert_pem', e.target.value)} rows={3} className="font-mono text-xs" /></div>
                <div><Label>Client Key (PEM)</Label><Textarea value={form.client_key_pem} onChange={e => set('client_key_pem', e.target.value)} rows={3} className="font-mono text-xs" /></div>
              </>
            )}
          </Section>
          
          {/* Section 3: Cài đặt luồng tải, ưu tiên định tuyến và trạng thái hoạt động */}
          <Section icon={<ShieldCheck className="size-4" />} title="Performance">
            <div className="grid gap-4 md:grid-cols-3">
              <div><Label>Max Connections</Label><Input type="number" value={form.max_connections} onChange={e => set('max_connections', parseInt(e.target.value) || 16)} /></div>
              <div><Label>Priority</Label><Input type="number" value={form.priority} onChange={e => set('priority', parseInt(e.target.value) || 100)} /></div>
              <div><Label>Weight</Label><Input type="number" value={form.weight} onChange={e => set('weight', parseInt(e.target.value) || 1)} /></div>
            </div>
            <div>
              <Label>Status</Label>
              <Select value={form.status} onValueChange={v => set('status', v)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="active">Active</SelectItem>
                  <SelectItem value="disabled">Disabled</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </Section>
          
          {/* Các nút điều khiển Lưu dữ liệu hoặc Hủy bỏ */}
          <div className="flex justify-end gap-3">
            <Button variant="outline" type="button" onClick={() => navigate({ to: '/mail', hash: 'endpoints' })}>Cancel</Button>
            <Button type="submit" disabled={saving}>
              {saving ? (
                <><Loader2 className="size-4 mr-2 animate-spin" />Saving...</>
              ) : (
                <><Save className="size-4 mr-2" />Save</>
              )}
            </Button>
          </div>
        </form>
      </div>
    </>
  )
}

// Bố cục khối Panel nhỏ gọn bọc các trường cấu hình theo chủ đề
function Section({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <div className="rounded-xl border bg-card p-6 space-y-4">
      <div className="flex items-center gap-2 text-sm font-bold text-muted-foreground">{icon}{title}</div>
      {children}
    </div>
  )
}
