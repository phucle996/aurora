import { request } from "../lib/api/fetcher";

export type NavigationItem = {
  key: string;
  actions: string[];
};

export type RenderContext = {
  navigation: NavigationItem[];
  capabilities: Record<string, boolean>;
  is_personal: boolean;
};

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export async function getRenderContext(signal?: AbortSignal): Promise<RenderContext> {
  const value = await request<unknown>("/me/iam/context/read", { method: "GET", signal });
  if (!isRecord(value) || !Array.isArray(value.navigation) || !isRecord(value.capabilities)) {
    throw new Error("IAM returned an invalid render context");
  }

  const navigation = value.navigation.map((item) => {
    if (!isRecord(item) || typeof item.key !== "string" || !Array.isArray(item.actions)) {
      throw new Error("IAM returned an invalid render navigation entry");
    }
    if (!item.actions.every((action) => typeof action === "string")) {
      throw new Error("IAM returned an invalid render navigation action");
    }
    return { key: item.key, actions: item.actions as string[] };
  });

  const capabilities: Record<string, boolean> = {};
  for (const [permission, enabled] of Object.entries(value.capabilities)) {
    if (typeof enabled !== "boolean") {
      throw new Error("IAM returned an invalid render capability");
    }
    // Capabilities preserve IAM's canonical five-level permission. Navigation is
    // only a presentation projection of module:object + behavior.
    capabilities[permission] = enabled;
  }
  if (typeof value.is_personal !== "boolean") {
    throw new Error("IAM returned an invalid principal render context");
  }

  return { navigation, capabilities, is_personal: value.is_personal };
}
