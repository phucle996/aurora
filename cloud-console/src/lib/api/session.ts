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
  is_personal: boolean;
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
  const res = await fetchJSON<{ data?: RenderContext }>("/api/v1/me/iam/context/read", {
    method: "GET",
    signal,
  });
  if (!res?.data) {
    throw new Error("Failed to load render context.");
  }
  return res.data;
}

export async function getUserProfile(signal?: AbortSignal): Promise<UserProfile> {
  const res = await fetchJSON<{ data?: UserProfile }>("/api/v1/me/iam/profile/read", {
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