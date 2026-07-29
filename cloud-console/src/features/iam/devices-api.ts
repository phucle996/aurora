import { fetchJSON } from "@/shared/api/http";

export type SelfDevice = {
  id: string;
  device_name: string;
  status: "active" | "online" | "revoked";
  is_online: boolean;
  is_current: boolean;
  last_seen_at?: string;
  last_seen_ip?: string;
  last_seen_user_agent?: string;
};

export async function getMyDevices(signal?: AbortSignal): Promise<{ items: SelfDevice[]; total: number }> {
  const response = await fetchJSON<{
    data?: {
      items?: Array<{
        device?: { id?: unknown; device_name?: unknown; status?: unknown };
        is_online?: unknown;
        is_current?: unknown;
        last_seen_at?: unknown;
        last_seen_ip?: unknown;
        last_seen_user_agent?: unknown;
      }>;
      total?: unknown;
    };
  }>("/api/v1/me/iam/device/read?limit=50&offset=0", {
    method: "GET",
    signal,
  });
  if (!Array.isArray(response.data?.items) || typeof response.data.total !== "number") {
    throw new Error("The device response is invalid.");
  }
  const items = response.data.items.map((item) => {
    const device = item.device;
    if (
      !device ||
      typeof device.id !== "string" ||
      typeof device.device_name !== "string" ||
      (device.status !== "active" && device.status !== "online" && device.status !== "revoked") ||
      typeof item.is_online !== "boolean" ||
      typeof item.is_current !== "boolean"
    ) {
      throw new Error("The device response is invalid.");
    }
    const status = device.status as SelfDevice["status"];
    return {
      id: device.id,
      device_name: device.device_name,
      status,
      is_online: item.is_online,
      is_current: item.is_current,
      last_seen_at: typeof item.last_seen_at === "string" ? item.last_seen_at : undefined,
      last_seen_ip: typeof item.last_seen_ip === "string" ? item.last_seen_ip : undefined,
      last_seen_user_agent: typeof item.last_seen_user_agent === "string" ? item.last_seen_user_agent : undefined,
    };
  });
  return { items, total: response.data.total };
}

export async function revokeMyDevice(deviceID: string): Promise<void> {
  await fetchJSON(`/api/v1/me/iam/device/delete/${encodeURIComponent(deviceID)}`, {
    method: "POST",
  });
}

export async function logoutOtherDevices(): Promise<number> {
  const response = await fetchJSON<{ data?: { revoked_sessions?: unknown } }>(
    "/api/v1/me/iam/device/delete-others",
    { method: "POST" },
  );
  if (typeof response.data?.revoked_sessions !== "number") {
    throw new Error("The device revocation response is invalid.");
  }
  return response.data.revoked_sessions;
}

// [COMMENT]: getUserDevices lấy danh sách thiết bị thực tế của user phục vụ platform audit
export async function getUserDevices(id: string, signal?: AbortSignal): Promise<{ items: Record<string, unknown>[]; total: number } | null> {
  try {
    const res = await fetchJSON<{ data?: { items?: Record<string, unknown>[]; total?: number } }>(`/api/v1/iam/users/${id}/devices`, {
      method: "GET",
      signal,
    });
    return {
      items: res?.data?.items || [],
      total: res?.data?.total || 0,
    };
  } catch (err) {
    console.error("Failed to fetch user devices", err);
    return null;
  }
}
