export function cloudConsoleRuntimeUrl(): string {
  const url = window.__AURORA_RUNTIME_CONFIG__?.cloudConsoleUrl?.trim();
  if (!url) throw new Error("Runtime config is missing cloudConsoleUrl");
  return url;
}

declare global {
  interface Window {
    __AURORA_RUNTIME_CONFIG__?: {
      cloudConsoleUrl?: string;
    };
  }
}
