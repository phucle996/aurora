"use client";

/**
 * WorkspaceInitializer.tsx — Cầu nối giữa UserSession và WorkspaceContext.
 *
 * Trigger: khi session authenticated + profile.user_id có sẵn (sau khi resolveUserSession chạy xong)
 * - Đọc zone_id từ zone catalog (gọi fetchZoneCatalog) + zone_code từ cookie (do ACR set)
 * - Gọi initWorkspaceContext với đúng context headers
 * - Khi session mất (logout) → clearWorkspaceContext
 *
 * Đặt component này bên trong cả <UserSessionProvider> và <WorkspaceProvider>.
 * Không render UI — chỉ quản lý side-effect.
 */

import { useEffect, useRef } from "react";
import { useUserSession } from "@/hooks/useUserSession";
import { useWorkspace } from "@/context/WorkspaceContext";
import { fetchZoneCatalog } from "@/lib/api/zone";

// [COMMENT]: Đọc giá trị cookie từ document.cookie — dùng để lấy zone_code ACR đã set
function readCookie(name: string): string | null {
  if (typeof document === "undefined") return null;
  const value = `; ${document.cookie}`;
  const parts = value.split(`; ${name}=`);
  if (parts.length === 2) return parts.pop()?.split(";").shift() ?? null;
  return null;
}

// [COMMENT]: WorkspaceInitializer lắng nghe khi profile đã load (user_id available),
// sau đó resolve zone_id từ catalog và gọi initWorkspaceContext.
// Dùng ref để tránh gọi lại trùng khi session re-render không đổi.
export function WorkspaceInitializer() {
  const { authenticated, loading, profile } = useUserSession();
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
    if (!userID) return;

    // [COMMENT]: Đọc zone_code từ cookie do ACR set sau khi login
    // Cookie name "zone_code" phải khớp với ACR session cookie
    const zoneCode = readCookie("zone_code");
    if (!zoneCode) return;

    // [COMMENT]: Context key để detect khi zone thay đổi (switch zone → cookie thay đổi)
    const contextKey = `${zoneCode}:personal`; // TODO: thêm tenant khi wired
    if (lastContextRef.current === contextKey) return;
    lastContextRef.current = contextKey;

    // [COMMENT]: Resolve zone_id từ catalog bằng zone_code, sau đó call workspace catalog
    void (async () => {
      try {
        const zones = await fetchZoneCatalog();
        const matched = zones.find(
          (z) => z.code.toLowerCase() === zoneCode.toLowerCase()
        );
        if (!matched) {
          console.warn("[WorkspaceInitializer] zone_code not found in catalog:", zoneCode);
          return;
        }

        await initWorkspaceContext({
          userID,
          zoneID: matched.id,
          // [COMMENT]: tenantID / roleID sẽ được bổ sung khi tenant context đầy đủ
        });
      } catch (err) {
        console.error("[WorkspaceInitializer] Failed to init workspace context:", err);
      }
    })();
  }, [authenticated, loading, profile, initWorkspaceContext, clearWorkspaceContext]);

  // [COMMENT]: Không render gì — chỉ là side-effect manager
  return null;
}
