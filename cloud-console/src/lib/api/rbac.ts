import { fetchJSON } from "./fetcher";

export type PlatformRoleItem = {
  id: string;
  code: string;
  name: string;
  role_level: number;
  description?: string; // [COMMENT]: Mô tả chi tiết vai trò thực tế từ backend
  scope: string;
  assignments_count: number;
  permissions_count: number;
  created_at: string;
  updated_at: string;
};

// [COMMENT]: listRoles lấy danh sách các vai trò platform hỗ trợ phân quyền
export async function listRoles(signal?: AbortSignal): Promise<PlatformRoleItem[]> {
  const res = await fetchJSON<{ data?: { roles?: PlatformRoleItem[] } }>("/api/v1/iam/rbac/role", {
    method: "GET",
    signal,
  });
  return res?.data?.roles || [];
}

export type PermissionItem = {
  id: string;
  module: string;
  object: string;
  behavior: string;
  description: string;
};

export type CreateRoleInput = {
  code: string;
  name: string;
  description: string;
  role_level: number;
  scope: string;
  permission_ids: string[];
};

// [COMMENT]: listPermissions lấy toàn bộ danh sách permissions catalog từ backend
export async function listPermissions(signal?: AbortSignal): Promise<PermissionItem[]> {
  const res = await fetchJSON<{ data?: { permissions?: PermissionItem[] } }>("/api/v1/iam/rbac/permissions", {
    method: "GET",
    signal,
  });
  return res?.data?.permissions || [];
}

// [COMMENT]: createRole gửi request tạo mới một vai trò kèm gán permissions
export async function createRole(input: CreateRoleInput, signal?: AbortSignal): Promise<void> {
  await fetchJSON("/api/v1/iam/rbac/role", {
    method: "POST",
    body: JSON.stringify(input),
    signal,
  });
}

// [COMMENT]: getUserRole lấy chi tiết vai trò của user mục tiêu kèm kiểm tra cấp bậc
export async function getUserRole(id: string, signal?: AbortSignal): Promise<PlatformRoleItem | null> {
  try {
    const res = await fetchJSON<{ data?: { role?: PlatformRoleItem } }>(`/api/v1/iam/users/${id}/roles`, {
      method: "GET",
      signal,
    });
    return res?.data?.role || null;
  } catch (err) {
    console.error("Failed to fetch user roles", err);
    return null;
  }
}

// [COMMENT]: assignUserRole gọi endpoint gán vai trò mới cho người dùng
export async function assignUserRole(userID: string, roleID: string, signal?: AbortSignal): Promise<void> {
  await fetchJSON("/api/v1/iam/rbac/user-role", {
    method: "POST",
    body: { user_id: userID, role_id: roleID },
    signal,
  });
}
