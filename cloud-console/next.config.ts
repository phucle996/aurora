import type { NextConfig } from "next";

function realtimeOrigin(): string {
  const configured = process.env.NEXT_PUBLIC_CENTRIFUGO_WS_URL?.trim();
  if (!configured) return "";
  try {
    const url = new URL(configured);
    return `${url.protocol}//${url.host}`;
  } catch {
    return "";
  }
}

const connectSources = ["'self'", realtimeOrigin()].filter(Boolean).join(" ");
const contentSecurityPolicy = [
  "default-src 'self'",
  "base-uri 'self'",
  "object-src 'none'",
  "frame-ancestors 'none'",
  "form-action 'self'",
  "script-src 'self' 'unsafe-inline'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob: https:",
  "font-src 'self' data:",
  `connect-src ${connectSources}`,
].join("; ");

const nextConfig: NextConfig = {
  poweredByHeader: false,
  async headers() {
    return [
      {
        source: "/:path*",
        headers: [
          { key: "Content-Security-Policy", value: contentSecurityPolicy },
          { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
          { key: "X-Content-Type-Options", value: "nosniff" },
          { key: "X-Frame-Options", value: "DENY" },
          { key: "Permissions-Policy", value: "camera=(), geolocation=(), microphone=(), payment=()" },
        ],
      },
    ];
  },
};

export default nextConfig;
