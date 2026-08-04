"use client";

import { useEffect } from "react";
import { usePathname, useRouter } from "next/navigation";

import { useUserSession } from "@/session/use-session";

export function LegacyConsoleRedirect() {
  const pathname = usePathname();
  const router = useRouter();
  const { status, renderContext } = useUserSession();

  useEffect(() => {
    if (status !== "authenticated" || !renderContext) return;
    const suffix = pathname === "/" ? "" : pathname;
    router.replace(`/${renderContext.kind}${suffix}`);
  }, [pathname, renderContext, router, status]);

  return <div className="min-h-[100svh] bg-background" aria-label="Resolving console context" />;
}
