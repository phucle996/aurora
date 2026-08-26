import { fetchJSON } from "@/shared/api/http";
import { criticalFetchJSON } from "@/shared/api/critical";

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

export type SelfMfaStatus = {
  status: "enabled" | "disabled";
  enabled_at?: string;
  recovery_codes_remaining: number;
};

export type MfaSetup = {
  setup_id: string;
  provisioning_uri: string;
  manual_secret: string;
  expires_at: string;
};

export type MfaConfirmation = {
  status: "enabled";
  enabled_at: string;
  recovery_codes: string[];
};

export async function getMyMfa(signal?: AbortSignal): Promise<SelfMfaStatus> {
  const response = await fetchJSON<{ data?: SelfMfaStatus }>("/api/v1/me/iam/mfa", {
    method: "GET",
    signal,
  });
  if (!response.data || (response.data.status !== "enabled" && response.data.status !== "disabled")) {
    throw new Error("The MFA status response is invalid.");
  }
  return response.data;
}

export async function startMyMfaSetup(): Promise<MfaSetup> {
  const response = await criticalFetchJSON<{ data?: MfaSetup }>("/api/v1/me/critical/iam/mfa/setup/start", {
    method: "POST",
  });
  if (!response.data?.setup_id || !response.data.manual_secret || !response.data.provisioning_uri) {
    throw new Error("The MFA setup response is invalid.");
  }
  return response.data;
}

export async function confirmMyMfaSetup(setupID: string, code: string): Promise<MfaConfirmation> {
  const response = await criticalFetchJSON<{ data?: MfaConfirmation }>(
    `/api/v1/me/critical/iam/mfa/setup/${encodeURIComponent(setupID)}/confirm`,
    { method: "POST", body: { code } },
  );
  if (!response.data || !Array.isArray(response.data.recovery_codes)) {
    throw new Error("The MFA confirmation response is invalid.");
  }
  return response.data;
}

export async function regenerateMyRecoveryCodes(code: string): Promise<string[]> {
  const response = await criticalFetchJSON<{ data?: { recovery_codes?: unknown } }>(
    "/api/v1/me/critical/iam/mfa/recovery/regenerate",
    { method: "POST", body: { code } },
  );
  if (!Array.isArray(response.data?.recovery_codes) || !response.data.recovery_codes.every((item) => typeof item === "string")) {
    throw new Error("The recovery-code response is invalid.");
  }
  return response.data.recovery_codes as string[];
}

export async function removeMyMfa(code: string): Promise<void> {
  await criticalFetchJSON("/api/v1/me/critical/iam/mfa", {
    method: "DELETE",
    body: { code },
  });
}
