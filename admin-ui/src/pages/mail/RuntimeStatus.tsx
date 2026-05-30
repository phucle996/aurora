import { useCallback, useState } from 'react'
import { Activity, CheckCircle2, Cpu, Server, Users, XCircle } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Fetch } from '@/lib/fetch'
import { usePageMeta } from '@/lib/page-meta'
import { cn } from '@/lib/utils'

// Định nghĩa kiểu dữ liệu phản hồi trạng thái hoạt động của Workers (WorkerStatus) từ hệ thống Mail
type WorkerStatus = {
  enabled: boolean; running: boolean; consumers: number; instance_id: string
  controller_instance_id: string; controller_epoch: number; assignment_generation: number
  fenced: boolean; last_error: string; in_flight: number
  instances: { id: string; hostname: string; status: string; capacity_workers: number; active_workers: number; current_epoch: number; last_heartbeat_at: string }[]
  consumer_state: { consumer_id: string; name: string; transport_type: string; current_workers: number; target_workers: number; lag: number; in_flight: number; error_rate: number; last_error: string; updated_at: string }[]
  broker_health: { key: string; transport: string; source: string; status: string; last_error: string }[]
}

// Hàm định dạng khoảng cách thời gian tương đối so với thời điểm hiện tại (ví dụ: "5m ago", "Just now")
function timeAgo(d: string) {
  if (!d) return '—'
  const diff = Date.now() - new Date(d).getTime()
  if (diff < 60000) return 'Just now'
  const m = Math.floor(diff / 60000)
  if (m < 60) return `${m}m ago`
  return `${Math.floor(m / 60)}h ago`
}

/**
 * Component hiển thị thông tin chi tiết về trạng thái vận hành của các Workers (Runtime Status)
 * Giúp đội ngũ vận hành hệ thống giám sát trực tiếp các Instance, Consumer Workers và sức khỏe Broker.
 */
export default function RuntimeStatusPage() {
  // Thiết lập metadata cho trang thái động của trình duyệt
  usePageMeta('Runtime Status | Aurora Admin', 'Inspect Mail worker runtime status and health details.')
  
  // State lưu trữ dữ liệu trạng thái thu thập được từ API
  const [status, setStatus] = useState<WorkerStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  // Hàm gọi API lấy dữ liệu trạng thái runtime hiện tại
  const load = useCallback(async () => {
    setLoading(true); setError('')
    try {
      const resp = await Fetch('/api/v1/runtime/worker-status')
      if (!resp.ok) { setError('Failed to load'); return }
      const body = await resp.json()
      // Tự động phân tích dữ liệu trả về phù hợp với cấu trúc APIResponse chuẩn
      setStatus(body.data ?? body)
    } catch { 
      setError('Failed to load') 
    } finally { 
      setLoading(false) 
    }
  }, [])

  // Khởi tạo gọi API một lần duy nhất ngay khi component mount vào cây DOM
  useState(() => { void load() })

  const s = status

  return (
    <>
      <div className="space-y-6 p-6">
        {/* Header trang và Trạng thái chung */}
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-2xl font-bold">Runtime Status</h1>
            <p className="text-sm text-muted-foreground">Mail worker runtime overview</p>
          </div>
          {s && (
            <Badge variant="outline" className={cn('text-sm font-bold px-3 py-1', s.running ? 'border-emerald-200 bg-emerald-50 text-emerald-700' : 'border-red-200 bg-red-50 text-red-700')}>
              {s.running ? 'Running' : 'Stopped'}
            </Badge>
          )}
        </div>

        {/* Thông báo lỗi nếu việc gọi API gặp trục trặc */}
        {error && <div className="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">{error}</div>}

        {s && (
          <>
            {/* Lưới các thông số tổng hợp quan trọng */}
            <div className="grid gap-4 md:grid-cols-4">
              {[
                { icon: <Activity className="size-4" />, label: 'Status', value: s.running ? 'Running' : 'Stopped', tone: s.running ? 'emerald' : 'red' },
                { icon: <Users className="size-4" />, label: 'Consumers', value: String(s.consumers), tone: 'blue' },
                { icon: <Cpu className="size-4" />, label: 'In-Flight', value: String(s.in_flight), tone: 'purple' },
                { icon: <Server className="size-4" />, label: 'Instances', value: String(s.instances?.length ?? 0), tone: 'amber' },
              ].map(m => (
                <div key={m.label} className="rounded-xl border bg-card p-4">
                  <div className="flex items-center gap-2 text-xs font-bold text-muted-foreground uppercase tracking-wider">{m.icon}{m.label}</div>
                  <p className="mt-2 text-2xl font-bold">{m.value}</p>
                </div>
              ))}
            </div>

            {/* Bảng liệt kê các Instances đang chạy và trạng thái Heartbeat */}
            {s.instances && s.instances.length > 0 && (
              <div className="rounded-xl border bg-card overflow-hidden">
                <div className="p-4 border-b"><h2 className="text-sm font-bold uppercase tracking-wider text-muted-foreground">Instances</h2></div>
                <Table>
                  <TableHeader>
                    <TableRow className="bg-muted/30">
                      <TableHead className="text-xs font-bold uppercase">ID</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Hostname</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Status</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Workers</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Heartbeat</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {s.instances.map(inst => (
                      <TableRow key={inst.id}>
                        <TableCell className="font-mono text-xs">{inst.id}</TableCell>
                        <TableCell>{inst.hostname}</TableCell>
                        <TableCell><Badge variant="outline" className="text-xs">{inst.status}</Badge></TableCell>
                        <TableCell>{inst.active_workers}/{inst.capacity_workers}</TableCell>
                        <TableCell className="text-xs text-muted-foreground">{timeAgo(inst.last_heartbeat_at)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}

            {/* Bảng thống kê chi tiết luồng xử lý của từng Consumer Workers */}
            {s.consumer_state && s.consumer_state.length > 0 && (
              <div className="rounded-xl border bg-card overflow-hidden">
                <div className="p-4 border-b"><h2 className="text-sm font-bold uppercase tracking-wider text-muted-foreground">Consumer Workers</h2></div>
                <Table>
                  <TableHeader>
                    <TableRow className="bg-muted/30">
                      <TableHead className="text-xs font-bold uppercase">Name</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Transport</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Workers</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Lag</TableHead>
                      <TableHead className="text-xs font-bold uppercase">In-Flight</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Error Rate</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Last Error</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {s.consumer_state.map(cs => (
                      <TableRow key={cs.consumer_id}>
                        <TableCell className="font-bold">{cs.name}</TableCell>
                        <TableCell className="text-xs">{cs.transport_type}</TableCell>
                        <TableCell>{cs.current_workers}/{cs.target_workers}</TableCell>
                        {/* Cảnh báo độ trễ cao nếu lag vượt quá 1000 thông điệp */}
                        <TableCell className={cn('font-bold', cs.lag > 1000 ? 'text-red-500' : '')}>{cs.lag}</TableCell>
                        <TableCell>{cs.in_flight}</TableCell>
                        {/* Cảnh báo tỷ lệ lỗi cao nếu tỷ lệ lỗi vượt quá 10% */}
                        <TableCell className={cn(cs.error_rate > 0.1 ? 'text-red-500 font-bold' : '')}>{(cs.error_rate * 100).toFixed(1)}%</TableCell>
                        <TableCell className="text-xs text-muted-foreground max-w-37.5 truncate">{cs.last_error || '—'}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}

            {/* Bảng giám sát sức khỏe của Broker (Kafka/RabbitMQ/Redis/...) */}
            {s.broker_health && s.broker_health.length > 0 && (
              <div className="rounded-xl border bg-card overflow-hidden">
                <div className="p-4 border-b"><h2 className="text-sm font-bold uppercase tracking-wider text-muted-foreground">Broker Health</h2></div>
                <Table>
                  <TableHeader>
                    <TableRow className="bg-muted/30">
                      <TableHead className="text-xs font-bold uppercase">Key</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Transport</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Source</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Status</TableHead>
                      <TableHead className="text-xs font-bold uppercase">Error</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {s.broker_health.map(bh => (
                      <TableRow key={bh.key}>
                        <TableCell className="font-mono text-xs">{bh.key}</TableCell>
                        <TableCell>{bh.transport}</TableCell>
                        <TableCell className="text-xs">{bh.source}</TableCell>
                        <TableCell>
                          <div className="flex items-center gap-1.5">
                            {bh.status === 'healthy' ? (
                              <CheckCircle2 className="size-3.5 text-emerald-500" />
                            ) : (
                              <XCircle className="size-3.5 text-red-500" />
                            )}
                            {bh.status}
                          </div>
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground max-w-50 truncate">{bh.last_error || '—'}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </>
        )}

        {/* Trạng thái Loading spinner */}
        {loading && (
          <div className="flex items-center justify-center h-32">
            <div className="size-8 animate-spin rounded-full border-4 border-primary border-t-transparent" />
          </div>
        )}
      </div>
    </>
  )
}
