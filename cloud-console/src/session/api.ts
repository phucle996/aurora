import { fetchJSON, type APIError } from "@/shared/api/http";

export type UserSession = {
  authenticated: true;
};

export type NavigationItem = {
  key: string;
  actions: string[];
};

type RenderContextBase = {
  navigation: NavigationItem[];
  capabilities: Record<string, boolean>;
};

export type RenderContext =
  | (RenderContextBase & { kind: "personal" })
  | (RenderContextBase & { kind: "tenant"; tenant_id: string });

export type UserProfile = {
  user_id: string;
  username: string;
  account_email: string;
  phone?: string;
  fullname: string;
  address?: string;
  avatar_url?: string;
  bio?: string;
  locale: string;
  timezone: string;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function decodeRenderContext(value: unknown): RenderContext {
  if (!isRecord(value) || !Array.isArray(value.navigation) || !isRecord(value.capabilities)) {
    throw new Error("Invalid render context returned by the server.");
  }

  const navigation = value.navigation.map((item) => {
    if (!isRecord(item) || typeof item.key !== "string" || !Array.isArray(item.actions)) {
      throw new Error("Invalid navigation entry returned by the server.");
    }
    if (!item.actions.every((action) => typeof action === "string")) {
      throw new Error("Invalid navigation action returned by the server.");
    }
    return { key: item.key, actions: item.actions as string[] };
  });

  const capabilities: Record<string, boolean> = {};
  for (const [key, enabled] of Object.entries(value.capabilities)) {
    if (typeof enabled !== "boolean") {
      throw new Error("Invalid capability returned by the server.");
    }
    capabilities[key] = enabled;
  }

  if (value.kind !== "personal" && value.kind !== "tenant") {
    throw new Error("Invalid principal context returned by the server.");
  }
  if (value.kind === "personal") {
    if ("tenant_id" in value) {
      throw new Error("Personal render context must not carry a tenant.");
    }
    return { navigation, capabilities, kind: "personal" };
  }
  if (typeof value.tenant_id !== "string" || value.tenant_id.length === 0) {
    throw new Error("Tenant render context is missing its verified tenant.");
  }
  return { navigation, capabilities, kind: "tenant", tenant_id: value.tenant_id };
}

function decodeProfile(value: unknown): UserProfile {
  if (
    !isRecord(value) ||
    typeof value.user_id !== "string" ||
    typeof value.username !== "string" ||
    typeof value.account_email !== "string" ||
    typeof value.fullname !== "string" ||
    typeof value.locale !== "string" ||
    typeof value.timezone !== "string"
  ) {
    throw new Error("Invalid user profile returned by the server.");
  }

  return {
    user_id: value.user_id,
    username: value.username,
    account_email: value.account_email,
    phone: typeof value.phone === "string" ? value.phone : undefined,
    fullname: value.fullname,
    address: typeof value.address === "string" ? value.address : undefined,
    avatar_url: typeof value.avatar_url === "string" ? value.avatar_url : undefined,
    bio: typeof value.bio === "string" ? value.bio : undefined,
    locale: value.locale,
    timezone: value.timezone,
  };
}

export async function getRenderContext(signal?: AbortSignal): Promise<RenderContext> {
  const response = await fetchJSON<{ data?: unknown }>("/api/v1/iam/context/read", {
    method: "GET",
    signal,
  });
  return decodeRenderContext(response.data);
}

export async function getUserProfile(signal?: AbortSignal): Promise<UserProfile> {
  const response = await fetchJSON<{ data?: unknown }>("/api/v1/me/iam/profile/read", {
    method: "GET",
    signal,
  });
  return decodeProfile(response.data);
}

export class UserUnauthorizedError extends Error {
  constructor() {
    super("User session is required.");
    this.name = "UserUnauthorizedError";
  }
}

export async function getUserSession(signal?: AbortSignal): Promise<UserSession> {
  try {
    const response = await fetchJSON<{ data?: { authenticated?: boolean } }>(
      "/api/v1/me/session",
      { method: "GET", signal },
    );
    if (response.data?.authenticated !== true) {
      throw new UserUnauthorizedError();
    }
    return { authenticated: true };
  } catch (error) {
    if ((error as APIError)?.status === 401) {
      throw new UserUnauthorizedError();
    }
    throw error;
  }
}
