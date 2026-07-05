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
  const res = await fetchJSON<{ data?: RenderContext }>("/api/v1/iam/context", {
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
    const res = await fetchJSON<{ data?: { authenticated?: boolean } }>("/api/v1/auth/session", {
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