import { Fetch } from '@/lib/fetch'

// [COMMENT]: Kiểu dữ liệu phản hồi báo cáo hạ tầng Mail của Zone từ Admin API
export type MailInfrastructureReport = {
  zone_id: string
  desired_state: string
  actual_state: string
  service_state: string
  fresh: boolean
  report_generation: number
  report_sequence: number
  capacity: Record<string, unknown>
  pending_items: number
  inventory_truncated: boolean
  reported_at: string
  expires_at: string
  dataplane_nodes: Array<Record<string, unknown>>
  stalwart_nodes: Array<Record<string, unknown>>
}

type APIResponse<T> = {
  data?: T
  message?: string
  error?: string
}

// [COMMENT]: Đọc và parse dữ liệu API response, quăng lỗi chuẩn hóa nếu HTTP status không thành công
async function readData<T>(resp: Response): Promise<T> {
  const body = (await resp.json().catch(() => ({}))) as APIResponse<T>
  if (!resp.ok) {
    throw new Error(body.message || body.error || 'Request failed')
  }
  return body.data as T
}

// [COMMENT]: Client API điều hướng truy vấn hạ tầng Mail Admin
export const mailAdminApi = {
  /**
   * Lấy báo cáo hạ tầng Mail của Zone đang chọn.
   * zoneID được truyền thông qua Header `X-Zone-Id` để khớp với Gateway/Context handling ở Controlplane Backend.
   */
  getInfrastructure: (zoneID: string) =>
    Fetch('/admin/mail/infrastructure', {
      headers: {
        'X-Zone-Id': zoneID,
      },
    }).then((r) => readData<MailInfrastructureReport>(r)),
}
