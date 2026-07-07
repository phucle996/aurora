"use client";

/**
 * UserSessionProvider.tsx — React Context Provider cho User Session.
 *
 * Quản lý vòng đời phiên làm việc (Session Lifecycle) ở mức root:
 *   - Gọi API để xác thực phiên khi tải trang lần đầu.
 *   - Lắng nghe sự kiện "iam:unauthorized" để huỷ phiên nếu có lỗi 401 từ server.
 *   - Lưu trữ cache module-level để tránh các cuộc gọi API trùng lặp trong SPA.
 */

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useRouter, usePathname } from "next/navigation";
import { toast } from "sonner";

import { getUserSession, getRenderContext, getUserProfile, UserUnauthorizedError, type UserSession, type RenderContext, type UserProfile } from "@/lib/api/session";
import {
  UserSessionContext,
  type UserSessionState,
  type UserSessionContextValue,
} from "@/hooks/user-session-context";

// [COMMENT]: Load cached context từ LocalStorage ở Client-side để render lập tức (Instant Render)
let localRenderContext: RenderContext | null = null;
let localProfile: UserProfile | null = null;

if (typeof window !== "undefined") {
  try {
    const r = localStorage.getItem("iam:render_context");
    if (r) localRenderContext = JSON.parse(r);
    const p = localStorage.getItem("iam:user_profile");
    if (p) localProfile = JSON.parse(p);
  } catch (e) {
    console.error("Failed to read from localStorage:", e);
  }
}

// [COMMENT]: Khởi tạo state mặc định cho phiên làm việc (chờ tải)
const initialState: UserSessionState = {
  loading: true,
  authenticated: localRenderContext !== null,
  session: localRenderContext ? { authenticated: true } : null,
  renderContext: localRenderContext,
  profile: localProfile,
  error: "",
  notice: "",
};

// [COMMENT]: Khởi tạo state khi chưa đăng nhập
const unauthenticatedState: UserSessionState = {
  loading: false,
  authenticated: false,
  session: null,
  renderContext: null,
  profile: null,
  error: "",
  notice: "",
};

// [COMMENT]: Thời gian tối đa chờ kiểm tra session (tránh treo giao diện)
const sessionLoadingTimeoutMs = 8000;

// [COMMENT]: Cache phiên làm việc tại cấp độ module để chia sẻ qua các lần mount/unmount
let cachedState: UserSessionState | null = null;

// [COMMENT]: Singleton promise lưu request kiểm tra session đang chạy, tránh race condition
let activeResolvePromise: Promise<UserSessionState> | null = null;

// [COMMENT]: Hàm builder tạo state khi đã đăng nhập thành công
function buildAuthenticatedState(
  session: UserSession,
  renderContext: RenderContext | null,
  profile: UserProfile | null
): UserSessionState {
  return {
    loading: false,
    authenticated: true,
    session,
    renderContext,
    profile,
    error: "",
    notice: "",
  };
}

// [COMMENT]: Hàm builder tạo state khi chưa đăng nhập hoặc lỗi
function buildUnauthenticatedState(overrides?: Partial<UserSessionState>): UserSessionState {
  return { ...unauthenticatedState, ...overrides };
}

// [COMMENT]: Thực hiện gọi API lấy thông tin phiên làm việc hiện tại và context hiển thị từ Edge/Controlplane
function resolveUserSession(): Promise<UserSessionState> {
  if (activeResolvePromise) return activeResolvePromise;

  activeResolvePromise = (async () => {
    let retryCount = 0;

    while (true) {
      try {
        // [COMMENT]: Bước 1 — Kiểm tra session TRƯỚC (sequential).
        // Chỉ khi authenticated: true mới tiếp tục fetch context/profile.
        // Gọi song song cả 3 là lãng phí khi user chưa đăng nhập vì
        // context/profile sẽ trả 401 → log nhiễu + tốn tài nguyên Edge.
        const session = await getUserSession();

        // [COMMENT]: Bước 2 — Chỉ gọi context + profile SAU KHI xác nhận đã đăng nhập.
        // Gọi song song nhau để tối ưu latency (không phụ thuộc lẫn nhau).
        const [renderCtx, profile] = await Promise.all([
          getRenderContext().catch((err) => {
            console.error("Failed to load render context, degrading gracefully:", err);
            return null;
          }),
          getUserProfile().catch((err) => {
            console.error("Failed to load user profile, degrading gracefully:", err);
            return null;
          }),
        ]);

        // [COMMENT]: Ghi đè cấu hình hiển thị và profile mới vào LocalStorage để phục vụ lần tải trang sau
        if (typeof window !== "undefined") {
          try {
            if (renderCtx) localStorage.setItem("iam:render_context", JSON.stringify(renderCtx));
            if (profile) localStorage.setItem("iam:user_profile", JSON.stringify(profile));
          } catch (e) {
            console.error("Failed to persist session cache to localStorage:", e);
          }
        }

        const nextState = buildAuthenticatedState(session, renderCtx, profile);
        cachedState = nextState;
        return nextState;
      } catch (error) {
        // [COMMENT]: Nếu lỗi 401 (chưa đăng nhập hoặc hết hạn), xoá sạch LocalStorage để bảo mật
        if (error instanceof UserUnauthorizedError) {
          if (typeof window !== "undefined") {
            try {
              localStorage.removeItem("iam:render_context");
              localStorage.removeItem("iam:user_profile");
            } catch (e) {
              console.error("Failed to clear session cache from localStorage:", e);
            }
          }
          cachedState = unauthenticatedState;
          return unauthenticatedState;
        }

        // [COMMENT]: Thử gọi lại tối đa 2 lần đối với lỗi network tạm thời
        if (retryCount < 2) {
          retryCount += 1;
          await new Promise((resolve) => window.setTimeout(resolve, 1500));
          continue;
        }

        // [COMMENT]: Hết lượt thử, trả về trạng thái unauthenticated kèm thông tin lỗi
        const failedState = buildUnauthenticatedState({
          error: error instanceof Error ? error.message : "Cannot verify user session.",
        });
        cachedState = failedState;
        return failedState;
      }
    }
  })();

  // [COMMENT]: Dọn dẹp reference promise sau khi hoàn thành để cho phép trigger lại
  activeResolvePromise.finally(() => {
    activeResolvePromise = null;
  });

  return activeResolvePromise;
}

export function UserSessionProvider({ children }: { children: ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();

  // [COMMENT]: Đọc state ban đầu từ cache module-level nếu đã tồn tại
  const [state, setState] = useState<UserSessionState>(() => cachedState ?? initialState);
  const mountedRef = useRef(true);

  // [COMMENT]: Tự động chuyển hướng người dùng nếu phiên bị huỷ (authenticated = false)
  useEffect(() => {
    if (!state.loading && !state.authenticated) {
      if (pathname && !pathname.startsWith("/signin")) {
        router.push("/signin");
      }
    }
  }, [state.loading, state.authenticated, pathname, router]);

  // [COMMENT]: Hiển thị thông báo khi hết hạn phiên làm việc và dọn dẹp notice
  useEffect(() => {
    if (state.notice === "session_expired") {
      toast.info("Session expired. Please sign in again.", {
        id: "user-session-expired",
        duration: 3200,
      });
      // Dọn dẹp notice ngay lập tức
      cachedState = {
        ...(cachedState ?? unauthenticatedState),
        notice: "",
      };
      if (mountedRef.current) {
        setState((current) => ({ ...current, notice: "" }));
      }
    }
  }, [state.notice]);

  // [COMMENT]: Lắng nghe sự kiện iam:unauthorized để tự động logout khi nhận mã 401
  useEffect(() => {
    const handleUnauthorized = () => {
      const nextState = buildUnauthenticatedState({ notice: "session_expired" });
      cachedState = nextState;
      if (mountedRef.current) setState(nextState);
    };

    window.addEventListener("iam:unauthorized", handleUnauthorized);
    return () => {
      window.removeEventListener("iam:unauthorized", handleUnauthorized);
    };
  }, []);

  // [COMMENT]: Resolve session lần đầu tiên khi ứng dụng mount ở root
  useEffect(() => {
    mountedRef.current = true;

    // [COMMENT]: Nếu session đã được resolve từ trước, không gọi lại API
    if (cachedState && !cachedState.loading) {
      return () => {
        mountedRef.current = false;
      };
    }

    // [COMMENT]: Thiết lập timeout phòng thủ đề phòng API bị treo vô hạn
    const timeoutID = window.setTimeout(() => {
      const timeoutState = buildUnauthenticatedState({
        error: "Session check timeout. Please sign in again.",
      });
      cachedState = timeoutState;
      if (mountedRef.current) setState(timeoutState);
    }, sessionLoadingTimeoutMs);

    // [COMMENT]: Chạy tiến trình xác thực phiên async
    void resolveUserSession()
      .then((nextState) => {
        window.clearTimeout(timeoutID);
        if (mountedRef.current) setState(nextState);
      })
      .catch((error) => {
        window.clearTimeout(timeoutID);
        if (mountedRef.current) {
          setState(
            buildUnauthenticatedState({
              error: error instanceof Error ? error.message : "Cannot verify user session.",
            })
          );
        }
      });

    return () => {
      window.clearTimeout(timeoutID);
      mountedRef.current = false;
    };
  }, []);

  // =========================================================================
  // ACTIONS: Các hàm tương tác phiên làm việc
  // =========================================================================

  // [COMMENT]: Hàm refresh session thủ công (ví dụ: nút retry hoặc sau khi đăng nhập)
  const refreshSession = useCallback(async () => {
    if (mountedRef.current) {
      setState((current) => ({ ...current, loading: true, error: "", notice: "" }));
    }

    try {
      const nextState = await resolveUserSession();
      if (mountedRef.current) setState(nextState);
      return nextState;
    } catch (error) {
      const failedState = buildUnauthenticatedState({
        error: error instanceof Error ? error.message : "Cannot verify user session.",
      });
      cachedState = failedState;
      if (mountedRef.current) setState(failedState);
      return failedState;
    }
  }, []);

  // [COMMENT]: Cập nhật state đăng nhập thành công trực tiếp (tiết kiệm 1 lượt API check session sau đăng nhập)
  const setAuthenticatedSession = useCallback((session: UserSession) => {
    const nextState = buildAuthenticatedState(session, null, null);
    cachedState = nextState;
    if (mountedRef.current) setState(nextState);
  }, []);

  // [COMMENT]: Xoá session (dùng sau khi gọi API logout thành công)
  const clearSession = useCallback(() => {
    if (typeof window !== "undefined") {
      try {
        localStorage.removeItem("iam:render_context");
        localStorage.removeItem("iam:user_profile");
      } catch (e) {
        console.error("Failed to clear session cache from localStorage:", e);
      }
    }
    cachedState = unauthenticatedState;
    if (mountedRef.current) setState(unauthenticatedState);
  }, []);

  // [COMMENT]: Tiêu thụ notice "session_expired" sau khi đã hiện thông báo toast
  const consumeNotice = useCallback(() => {
    cachedState = {
      ...(cachedState ?? unauthenticatedState),
      notice: "",
    };
    if (mountedRef.current) {
      setState((current) => ({ ...current, notice: "" }));
    }
  }, []);

  // [COMMENT]: checkPermission kiểm tra xem user có quyền thực hiện hành động trên đối tượng hay không
  const checkPermission = useCallback((matchKey: string, action: string): boolean => {
    const navs = state.renderContext?.navigation;
    if (!navs) return false;

    // 1. Kiểm tra tài khoản Super Admin (wildcard Key "*")
    const superAdmin = navs.find(n => n.key === "*");
    if (superAdmin && superAdmin.actions.includes("*")) {
      return true;
    }

    const matchParts = matchKey.split(":");
    if (matchParts.length !== 4) return false;

    for (const nav of navs) {
      const navParts = nav.key.split(":");
      if (navParts.length !== 4) continue;

      const isMatch = matchParts.every((part, i) => part === "*" || part === navParts[i]);
      if (isMatch) {
        // Kiểm tra action cụ thể hoặc wildcard action
        if (nav.actions.includes(action) || nav.actions.includes("*")) {
          return true;
        }
      }
    }

    return false;
  }, [state.renderContext]);

  // [COMMENT]: Memoize value để tối ưu hoá hiệu năng render của các component con
  const value = useMemo<UserSessionContextValue>(
    () => ({
      ...state,
      refreshSession,
      setAuthenticatedSession,
      clearSession,
      consumeNotice,
      checkPermission,
    }),
    [clearSession, consumeNotice, refreshSession, setAuthenticatedSession, checkPermission, state]
  );

  return <UserSessionContext.Provider value={value}>{children}</UserSessionContext.Provider>;
}
