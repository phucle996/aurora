import { fetchJSON } from "./fetcher";

export type LoginRequest = {
  username: string;
  password: string;
  device_public_key: string;
};

export type RegisterRequest = {
  username: string;
  email: string;
  password: string;
  re_password: string;
  fullname: string;
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

export const authAPI = {
  login,
  register,
};
