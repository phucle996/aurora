import { fetchJSON } from "@/shared/api/http";

export type PlatformMfaStatus = {
  mfa_enabled: boolean;
  created_at?: string;
};

// [COMMENT]: Gọi API lấy thông tin trạng thái MFA của một user bất kỳ dành cho quản trị viên/kiểm toán
export async function getUserMfaPlatform(id: string, signal?: AbortSignal): Promise<PlatformMfaStatus | null> {
  const res = await fetchJSON<{ data?: PlatformMfaStatus }>(`/api/v1/iam/users/${id}/mfa`, {
    method: "GET",
    signal,
  });
  return res?.data || null;
}
