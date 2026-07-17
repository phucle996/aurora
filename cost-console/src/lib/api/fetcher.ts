const BASE_URL = import.meta.env.VITE_API_BASE_URL;

if (BASE_URL === undefined) {
  throw new Error("VITE_API_BASE_URL is not defined in environment variables. Please check your .env file.");
}

export async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const url = `${BASE_URL}${path}`;
  const response = await fetch(url, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options?.headers || {}),
    },
  });

  if (!response.ok) {
    const errorText = await response.text();
    let errorJson;
    try {
      errorJson = JSON.parse(errorText);
    } catch {
      // Ignored
    }
    throw new Error(errorJson?.message || errorJson?.error || `HTTP error ${response.status}`);
  }

  if (response.status === 204) {
    return {} as T;
  }

  const resJson = await response.json();
  return resJson.data as T;
}
