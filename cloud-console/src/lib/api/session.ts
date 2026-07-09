import { fetchJSON, type APIError } from "./fetcher";

export type UserSession = {
  authenticated: true;
};

export type NavigationItem = {
  key: string;
  actions: string[];
};

export type RenderContext = {
  navigation: NavigationItem[];
  capabilities: Record<string, boolean>;
};

export type UserProfile = {
  user_id: string;
  fullname: string;
  avatar_url?: string;
  bio?: string;
  locale: string;
  timezone: string;
};

export async function getRenderContext(signal?: AbortSignal): Promise<RenderContext> {
  const res = await fetchJSON<{ data?: RenderContext }>("/api/v1/me/context", {
    method: "GET",
    signal,
  });
  if (!res?.data) {
    throw new Error("Failed to load render context.");
  }
  return res.data;
}

export async function getUserProfile(signal?: AbortSignal): Promise<UserProfile> {
  const res = await fetchJSON<{ data?: UserProfile }>("/api/v1/me/profile", {
    method: "GET",
    signal,
  });
  if (!res?.data) {
    throw new Error("Failed to load user profile.");
  }
  return res.data;
}

export class UserUnauthorizedError extends Error {
  constructor() {
    super("User session is required.");
    this.name = "UserUnauthorizedError";
  }
}

export async function getUserSession(signal?: AbortSignal): Promise<UserSession> {
  try {
    const res = await fetchJSON<{ data?: { authenticated?: boolean } }>("/api/v1/me/session", {
      method: "GET",
      signal,
    });
    if (res?.data?.authenticated !== true) {
      throw new UserUnauthorizedError();
    }
    return { authenticated: true };
  } catch (error) {
    const apiErr = error as APIError;
    if (apiErr?.status === 401) {
      throw new UserUnauthorizedError();
    }
    throw error;
  }
}

export type AdminUserItem = {
  id: string;
  username: string;
  email: string;
  status: string;
  role?: string;
  mfa_enabled?: boolean;
  devices_count?: number;
  created_at: string;
  updated_at: string;
};

// [COMMENT]: listAdminUsers gửi request lấy danh sách người dùng cho Admin portal
export async function listAdminUsers(limit = 20, offset = 0, signal?: AbortSignal): Promise<AdminUserItem[]> {
  const res = await fetchJSON<{ data?: { users?: AdminUserItem[] } }>(`/api/v1/iam/users?limit=${limit}&offset=${offset}`, {
    method: "GET",
    signal,
  });
  return res?.data?.users || [];
}

// [COMMENT]: deleteAdminUser gọi endpoint xóa người dùng theo ID
export async function deleteAdminUser(id: string, signal?: AbortSignal): Promise<void> {
  await fetchJSON(`/api/v1/iam/users/${id}`, {
    method: "DELETE",
    signal,
  });
}

export type PlatformRoleItem = {
  id: string;
  code: string;
  name: string;
  role_level: number;
  description?: string; // [COMMENT]: Mô tả chi tiết vai trò thực tế từ backend
  scope: string;
};

// [COMMENT]: listPlatformRoles lấy danh sách các vai trò platform hỗ trợ phân quyền
export async function listPlatformRoles(signal?: AbortSignal): Promise<PlatformRoleItem[]> {
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

// [COMMENT]: getUserRolePlatform lấy chi tiết vai trò của user mục tiêu kèm kiểm tra cấp bậc
export async function getUserRolePlatform(id: string, signal?: AbortSignal): Promise<PlatformRoleItem | null> {
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