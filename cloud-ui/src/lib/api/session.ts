import { fetchJSON, type APIError } from "./fetcher";

export type UserSession = {
  authenticated: true;
};

export class UserUnauthorizedError extends Error {
  constructor() {
    super("User session is required.");
    this.name = "UserUnauthorizedError";
  }
}

export async function getUserSession(signal?: AbortSignal): Promise<UserSession> {
  try {
    // [COMMENT]: Gọi trực tiếp API Endpoint v1 tại Envoy Gateway để kiểm tra phiên làm việc
    await fetchJSON<{ data?: { authenticated?: boolean } }>("/api/v1/auth/session", {
      method: "GET",
      signal,
    });
    return { authenticated: true };
  } catch (error) {
    const apiErr = error as APIError;
    if (apiErr?.status === 401) {
      throw new UserUnauthorizedError();
    }
    throw error;
  }
}
