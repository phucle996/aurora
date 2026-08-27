import { NextResponse } from "next/server";

export function proxy() {
  const zonePublicBaseDomain = (
    process.env.AURORA_ZONE_PUBLIC_BASE_DOMAIN ?? process.env.NEXT_PUBLIC_ZONE_PUBLIC_BASE_DOMAIN
  )
    ?.trim()
    .toLowerCase();
  if (
    !zonePublicBaseDomain ||
    zonePublicBaseDomain.length > 253 ||
    !zonePublicBaseDomain
      .split(".")
      .every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))
  ) {
    return new NextResponse("Cloud Console runtime configuration is unavailable", { status: 503 });
  }
  let centrifugoWsOrigin: string;
  try {
    const candidate = new URL(process.env.AURORA_CENTRIFUGO_WS_ORIGIN ?? "");
    if (!["wss:", "ws:"].includes(candidate.protocol) || candidate.username || candidate.password) {
      throw new Error("invalid Centrifugo WebSocket origin");
    }
    centrifugoWsOrigin = candidate.origin;
  } catch {
    return new NextResponse("Cloud Console runtime configuration is unavailable", { status: 503 });
  }

  const response = NextResponse.next();
  response.headers.set(
    "Content-Security-Policy",
    [
      "default-src 'self'",
      "base-uri 'self'",
      "object-src 'none'",
      "frame-ancestors 'none'",
      "form-action 'self'",
      "script-src 'self' 'unsafe-inline'",
      "style-src 'self' 'unsafe-inline'",
      "img-src 'self' data: blob: https:",
      "font-src 'self' data:",
      `connect-src 'self' https://*.${zonePublicBaseDomain} ${centrifugoWsOrigin}`,
    ].join("; "),
  );
  return response;
}

export const config = {
  matcher: "/((?!api|_next/static|_next/image|favicon.ico|runtime-config.js).*)",
};
