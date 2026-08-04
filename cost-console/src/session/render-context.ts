import { request } from "../lib/api/fetcher";

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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export async function getRenderContext(signal?: AbortSignal): Promise<RenderContext> {
  const value = await request<unknown>("/iam/context/read", { method: "GET", signal });
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
  if (value.kind !== "personal" && value.kind !== "tenant") {
    throw new Error("IAM returned an invalid principal render context");
  }
  if (value.kind === "personal") {
    if ("tenant_id" in value) {
      throw new Error("IAM returned a tenant-bound personal render context");
    }
    return { navigation, capabilities, kind: "personal" };
  }
  if (typeof value.tenant_id !== "string" || value.tenant_id.length === 0) {
    throw new Error("IAM returned a tenant render context without tenant_id");
  }
  return { navigation, capabilities, kind: "tenant", tenant_id: value.tenant_id };
}
