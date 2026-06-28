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

import { getUserSession, UserUnauthorizedError, type UserSession } from "@/lib/api/session";
import {
  UserSessionContext,
  type UserSessionState,
  type UserSessionContextValue,
} from "@/hooks/user-session-context";

// [COMMENT]: Khởi tạo state mặc định cho phiên làm việc (chờ tải)
const initialState: UserSessionState = {
  loading: true,
  authenticated: false,
  session: null,
  error: "",
  notice: "",
};

// [COMMENT]: Khởi tạo state khi chưa đăng nhập
const unauthenticatedState: UserSessionState = {
  loading: false,
  authenticated: false,
  session: null,
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
function buildAuthenticatedState(session: UserSession): UserSessionState {
  return {
    loading: false,
    authenticated: true,
    session,
    error: "",
    notice: "",
  };
}

// [COMMENT]: Hàm builder tạo state khi chưa đăng nhập hoặc lỗi
function buildUnauthenticatedState(overrides?: Partial<UserSessionState>): UserSessionState {
  return { ...unauthenticatedState, ...overrides };
}

// [COMMENT]: Thực hiện gọi API lấy thông tin phiên làm việc hiện tại từ Edge/Controlplane
function resolveUserSession(): Promise<UserSessionState> {
  if (activeResolvePromise) return activeResolvePromise;

  activeResolvePromise = (async () => {
    let retryCount = 0;

    while (true) {
      try {
        const session = await getUserSession();
        const nextState = buildAuthenticatedState(session);
        cachedState = nextState;
        return nextState;
      } catch (error) {
        // [COMMENT]: Nếu lỗi 401 (chưa đăng nhập), chuyển thẳng về unauthenticated state
        if (error instanceof UserUnauthorizedError) {
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
  // [COMMENT]: Đọc state ban đầu từ cache module-level nếu đã tồn tại
  const [state, setState] = useState<UserSessionState>(() => cachedState ?? initialState);
  const mountedRef = useRef(true);

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
    const nextState = buildAuthenticatedState(session);
    cachedState = nextState;
    if (mountedRef.current) setState(nextState);
  }, []);

  // [COMMENT]: Xoá session (dùng sau khi gọi API logout thành công)
  const clearSession = useCallback(() => {
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

  // [COMMENT]: Memoize value để tối ưu hoá hiệu năng render của các component con
  const value = useMemo<UserSessionContextValue>(
    () => ({
      ...state,
      refreshSession,
      setAuthenticatedSession,
      clearSession,
      consumeNotice,
    }),
    [clearSession, consumeNotice, refreshSession, setAuthenticatedSession, state]
  );

  return <UserSessionContext.Provider value={value}>{children}</UserSessionContext.Provider>;
}
