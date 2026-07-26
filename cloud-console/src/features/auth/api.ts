import { fetchJSON } from "@/shared/api/http";

export type LoginRequest = {
  username: string;
  password: string;
  device_public_key: string;
  trust_device: boolean;
  // [COMMENT]: zone_code đại diện cho zone mà user lựa chọn để đăng nhập (ví dụ: "vn", "sg")
  zone_code: string;
  // [COMMENT]: tenant_domain được điền khi user nhập username@tenant_domain.
  // Undefined = đăng nhập global context, có giá trị = đăng nhập tenant context.
  tenant_domain?: string;
  session_proof_challenge_id: string;
  session_proof_timestamp: number;
  session_proof_signature: string;
};

export type SessionProofChallenge = {
  challenge_id: string;
  nonce: string;
  expires_in: number;
};

export async function requestLoginChallenge(
  options: { signal?: AbortSignal } = {},
): Promise<SessionProofChallenge> {
  return fetchJSON<SessionProofChallenge>("/api/v1/auth/login/challenge", {
    method: "POST",
    credentials: "include",
    signal: options.signal,
  });
}

export type RegisterRequest = {
  username: string;
  email: string;
  password: string;
  fullname: string;
  phone?: string;
  location?: string;
  timezone?: string;
};

export type VerifyAccountRequest = {
	user_id: string;
	event_id: string;
	token: string;
};

export async function login(
  payload: LoginRequest,
  options: { signal?: AbortSignal } = {},
): Promise<void> {
  await fetchJSON<void>("/api/v1/auth/login", {
    method: "POST",
    body: payload,
    credentials: "include",
    signal: options.signal,
  });
}

export async function register(
  payload: RegisterRequest,
  options: { signal?: AbortSignal } = {},
): Promise<void> {
  await fetchJSON<void>("/api/v1/auth/register", {
    method: "POST",
    body: payload,
    credentials: "include",
    signal: options.signal,
  });
}

export async function verifyAccount(
	payload: VerifyAccountRequest,
	options: { signal?: AbortSignal } = {},
): Promise<void> {
	await fetchJSON<void>("/api/v1/auth/verify", {
		method: "POST",
		body: payload,
		credentials: "same-origin",
		signal: options.signal,
	});
}

// [COMMENT]: Gọi logout endpoint để xoá runtime session trong Redis + revoke refresh token trong DB.
// Best-effort: nếu thất bại (network error, 401) vẫn tiếp tục clear client state.
export async function logout(
  options: { signal?: AbortSignal } = {},
): Promise<void> {
  try {
    await fetchJSON<void>("/api/v1/auth/logout", {
      method: "POST",
      credentials: "include",
      signal: options.signal,
    });
  } catch {
    // [COMMENT]: Logout thất bại ở server nhưng client vẫn cần clear local state.
    // Không throw — caller sẽ tự clear localStorage và redirect.
  }
}

export const authAPI = {
  login,
  requestLoginChallenge,
  register,
	verifyAccount,
  logout,
};
