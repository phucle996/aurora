// Cost Console and Cost Manager share the same Envoy origin. Keep the API
// prefix explicit so an omitted local env value cannot crash the SPA during
// module evaluation or accidentally route `/billing/*` back to the frontend.
const BASE_URL = (import.meta.env.VITE_API_BASE_URL?.trim() || "/api/v1").replace(/\/+$/, "");

export async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const url = `${BASE_URL}${path}`;
  const headers = new Headers(options?.headers);
  if (!headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  headers.set('X-Requested-With', 'XMLHttpRequest');
  const response = await fetch(url, {
    ...options,
    credentials: 'same-origin',
    headers,
  });

  if (!response.ok) {
    const errorText = await response.text();
    let errorJson;
    try {
      const normalizedError = errorText.startsWith(")]}',\n")
        ? errorText.slice(6)
        : errorText.startsWith(")]}',") ? errorText.slice(5) : errorText;
      errorJson = JSON.parse(normalizedError);
    } catch {
      // Ignored
    }
    throw new Error(errorJson?.message || errorJson?.error_message || errorJson?.error || `HTTP error ${response.status}`);
  }

  if (response.status === 204) {
    return {} as T;
  }

  // [COMMENT]: Chấp nhận XSSI prefix của edge trong khi vẫn giữ response envelope nhất quán.
  const responseText = await response.text();
  const normalized = responseText.startsWith(")]}',\n")
    ? responseText.slice(6)
    : responseText.startsWith(")]}',")
      ? responseText.slice(5)
      : responseText;
  const resJson = JSON.parse(normalized);
  return (resJson.data === undefined ? resJson : resJson.data) as T;
}
