import { fetchJSON } from "@/shared/api/http";
import { criticalFetchJSON } from "@/shared/api/critical";

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

export type ExternalIdentitySummary = {
  provider: "google" | "github";
  state: "not_linked" | "linked";
  provider_email?: string;
  email_verified_at?: string | null;
  last_login_at?: string | null;
  linked_at?: string | null;
};

export type UserAuthMethods = {
  account_identifier_email: string;
  password_set: boolean;
  google: ExternalIdentitySummary;
  github: ExternalIdentitySummary;
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
  await criticalFetchJSON(`/api/v1/critical/iam/users/${id}/status`, {
	method: "PUT",
	body: { status },
	signal,
  });
}

// [COMMENT]: resetUserPassword gọi endpoint reset mật khẩu của người dùng bởi Admin
export async function resetUserPassword(id: string, password: string, signal?: AbortSignal): Promise<void> {
  await criticalFetchJSON(`/api/v1/critical/iam/users/${id}/password`, {
    method: "PUT",
    body: { password },
    signal,
  });
}

export async function getUserAuthMethods(id: string, signal?: AbortSignal): Promise<UserAuthMethods> {
  const res = await fetchJSON<{ data?: UserAuthMethods }>(`/api/v1/iam/users/${id}/auth-methods`, {
    method: "GET",
    signal,
  });
  if (!res?.data) {
    throw new Error("Authentication methods are unavailable");
  }
  return res.data;
}
