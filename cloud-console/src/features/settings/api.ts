import { criticalFetchJSON } from "@/shared/api/critical";
import { fetchJSON } from "@/shared/api/http";

export type UpdateProfileInput = {
  fullname: string;
  phone: string;
  address: string;
  avatar_url: string;
  bio: string;
  locale: string;
  timezone: string;
};

export type SocialProvider = "google" | "github";

export type SocialLink = {
  provider: SocialProvider;
  state: "not_linked" | "linked" | "revoked";
  provider_email?: string;
  email_verified_at?: string;
  last_login_at?: string;
  linked_at?: string;
  revoked_at?: string;
};

export async function updateMyProfile(input: UpdateProfileInput): Promise<void> {
  await fetchJSON("/api/v1/me/iam/profile", {
    method: "PATCH",
    body: input,
  });
}

export async function getMySocialLinks(signal?: AbortSignal): Promise<SocialLink[]> {
  const response = await fetchJSON<{ data?: { items?: unknown } }>("/api/v1/me/iam/social-link", {
    method: "GET",
    signal,
  });
  if (!Array.isArray(response.data?.items)) {
    throw new Error("The social-link response is invalid.");
  }

  return response.data.items.map((value) => {
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
      throw new Error("The social-link response is invalid.");
    }
    const item = value as Record<string, unknown>;
    if (
      (item.provider !== "google" && item.provider !== "github") ||
      (item.state !== "not_linked" && item.state !== "linked" && item.state !== "revoked")
    ) {
      throw new Error("The social-link response is invalid.");
    }
    return {
      provider: item.provider,
      state: item.state,
      provider_email: typeof item.provider_email === "string" ? item.provider_email : undefined,
      email_verified_at: typeof item.email_verified_at === "string" ? item.email_verified_at : undefined,
      last_login_at: typeof item.last_login_at === "string" ? item.last_login_at : undefined,
      linked_at: typeof item.linked_at === "string" ? item.linked_at : undefined,
      revoked_at: typeof item.revoked_at === "string" ? item.revoked_at : undefined,
    };
  });
}

export async function startSocialLink(provider: SocialProvider): Promise<string> {
  const response = await criticalFetchJSON<{ authorization_url?: unknown }>(
    `/api/v1/critical/me/iam/social-link/${provider}/start`,
    {
      method: "POST",
      body: { return_to: "/settings/social-links" },
    },
  );
  if (typeof response.authorization_url !== "string" || !response.authorization_url.startsWith("https://")) {
    throw new Error("The social provider authorization URL is invalid.");
  }
  return response.authorization_url;
}

export async function unlinkSocialLink(provider: SocialProvider): Promise<void> {
  await criticalFetchJSON(`/api/v1/critical/me/iam/social-link/${provider}`, {
    method: "DELETE",
  });
}
