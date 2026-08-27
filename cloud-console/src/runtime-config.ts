export type PublicRuntimeConfig = {
  envoyUrl: string;
  centrifugoWsUrl: string;
  costConsoleUrl: string;
  zonePublicBaseDomain: string;
};

declare global {
  interface Window {
    __AURORA_RUNTIME_CONFIG__?: PublicRuntimeConfig;
  }
}

export function publicRuntimeConfig(): PublicRuntimeConfig | null {
  if (typeof window === "undefined") return null;
  return window.__AURORA_RUNTIME_CONFIG__ ?? null;
}
