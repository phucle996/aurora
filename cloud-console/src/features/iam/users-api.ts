import { fetchJSON } from "@/shared/api/http";

export type PlatformUserItem = {
  id: string;
  username: string;
  email: string;
  status: string;
  role?: string;
  mfa_enabled?: boolean;
  devices_count?: number;
  bio?: string | null;
  fullname?: string | null;
  last_seen_ip?: string | null;   // [COMMENT]: IP gần nhất từ device thực tế
  last_seen_at?: string | null;   // [COMMENT]: Thời điểm hoạt động gần nhất
  created_at: string;
  updated_at: string;
};

// [COMMENT]: listUsers gửi request lấy danh sách người dùng cho Admin portal
export async function listUsers(limit = 20, offset = 0, signal?: AbortSignal): Promise<PlatformUserItem[]> {
  const res = await fetchJSON<{ data?: { users?: PlatformUserItem[] } }>(`/api/v1/iam/users?limit=${limit}&offset=${offset}`, {
    method: "GET",
    signal,
  });
  return res?.data?.users || [];
}

// [COMMENT]: updateUserStatus gọi endpoint cập nhật trạng thái hoạt động của người dùng
export async function updateUserStatus(id: string, status: string, signal?: AbortSignal): Promise<void> {
  await fetchJSON(`/api/v1/iam/users/${id}/status?status=${status}`, {
    method: "PUT",
    signal,
  });
}

// [COMMENT]: resetUserPassword gọi endpoint reset mật khẩu của người dùng bởi Admin
export async function resetUserPassword(id: string, password: string, signal?: AbortSignal): Promise<void> {
  await fetchJSON(`/api/v1/iam/users/${id}/password`, {
    method: "PUT",
    body: { password },
    signal,
  });
}
