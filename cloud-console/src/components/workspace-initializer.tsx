"use client";

/**
 * WorkspaceInitializer.tsx — Cầu nối giữa UserSession và WorkspaceContext.
 *
 * Trigger: khi session authenticated + profile.user_id có sẵn (sau khi resolveUserSession chạy xong)
 * - Đọc Zone context từ the verified session/cookie boundary (never forwards
 *   actor or Zone identifiers as authorization claims)
 * - Gọi initWorkspaceContext để lấy catalog workspace authoritative
 * - Khi session mất (logout) → clearWorkspaceContext
 *
 * Đặt component này bên trong cả <UserSessionProvider> và <WorkspaceProvider>.
 * Không render UI — chỉ quản lý side-effect.
 */

import { useEffect, useRef } from "react";
import { useUserSession } from "@/session/use-session";
import { useWorkspace } from "@/context/WorkspaceContext";

// [COMMENT]: Đọc giá trị cookie từ document.cookie — dùng để lấy zone_code ACR đã set
function readCookie(name: string): string | null {
  if (typeof document === "undefined") return null;
  const value = `; ${document.cookie}`;
  const parts = value.split(`; ${name}=`);
  if (parts.length === 2) {
    try {
      return decodeURIComponent(parts.pop()?.split(";").shift() ?? "") || null;
    } catch {
      return null;
    }
  }
  return null;
}

// [COMMENT]: WorkspaceInitializer lắng nghe khi profile đã load (user_id available),
// sau đó resolve zone_id từ catalog và gọi initWorkspaceContext.
// Dùng ref để tránh gọi lại trùng khi session re-render không đổi.
export function WorkspaceInitializer() {
  const { authenticated, loading, profile, renderContext } = useUserSession();
  const { initWorkspaceContext, clearWorkspaceContext } = useWorkspace();

  // [COMMENT]: Track context key (zone_code:tenant) để detect thay đổi và tránh double-init
  const lastContextRef = useRef<string | null>(null);

  useEffect(() => {
    if (loading) return;

    if (!authenticated) {
      // [COMMENT]: Logout hoặc session hết hạn → dọn sạch workspace state và reset key
      lastContextRef.current = null;
      clearWorkspaceContext();
      return;
    }

    // [COMMENT]: user_id bắt buộc từ profile — nếu profile chưa load xong thì chờ
    const userID = profile?.user_id;
    if (!userID || !renderContext) return;

    // [COMMENT]: Đọc zone_code từ cookie do ACR set sau khi login
    // Cookie name "zone_code" phải khớp với ACR session cookie
    const zoneCode = readCookie("zone_code");
    if (!zoneCode) return;

    // [COMMENT]: Context key để detect khi zone thay đổi (switch zone → cookie thay đổi)
    const ownerMode = renderContext.is_personal ? "personal" : "tenant";
    const contextKey = `${zoneCode}:${ownerMode}`;
    if (lastContextRef.current === contextKey) return;
    lastContextRef.current = contextKey;

    // The backend derives actor and Zone from the verified session/cookie; the
    // Console never forwards those values as authorization claims.
    void (async () => {
      try {
        await initWorkspaceContext({ tenantContext: !renderContext.is_personal });
      } catch (err) {
        console.error("[WorkspaceInitializer] Failed to init workspace context:", err);
      }
    })();
  }, [authenticated, loading, profile, renderContext, initWorkspaceContext, clearWorkspaceContext]);

  // [COMMENT]: Không render gì — chỉ là side-effect manager
  return null;
}
