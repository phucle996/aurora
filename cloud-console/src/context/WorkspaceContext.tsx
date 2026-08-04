"use client";

/**
 * WorkspaceContext.tsx — React Context cho Workspace Selection.
 *
 * Quản lý:
 *   - Danh sách catalog workspace trong context hiện tại (zone + personal/tenant)
 *   - Workspace đang được chọn (lưu vào cookie workspace_id)
 *   - Logic init: call catalog sau login, set cookie workspace_id nếu chưa có
 *   - Logic switch context: khi đổi zone hoặc tenant → clear cookie + reload catalog
 */

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { fetchWorkspaceCatalog, type WorkspaceCatalogItem } from "@/features/workspaces/api";
import { useQueryClient } from "@tanstack/react-query";

// ─── Cookie helpers ───────────────────────────────────────────────────────────

const COOKIE_KEY = "workspace_id";

// [COMMENT]: Đọc workspace_id từ cookie — trả về null nếu không tồn tại
function getCookieWorkspaceID(): string | null {
  if (typeof document === "undefined") return null;
  const match = document.cookie
    .split("; ")
    .find((row) => row.startsWith(`${COOKIE_KEY}=`));
  if (!match) return null;
  try {
    return decodeURIComponent(match.split("=")[1]);
  } catch {
    return null;
  }
}

// [COMMENT]: Ghi workspace_id vào cookie Strict/Secure, expires 30 ngày
function setCookieWorkspaceID(id: string): void {
  if (typeof document === "undefined") return;
  const maxAge = 30 * 24 * 60 * 60; // 30 ngày
  const secure = window.location.protocol === "https:" ? "; Secure" : "";
  document.cookie = `${COOKIE_KEY}=${encodeURIComponent(id)}; path=/; max-age=${maxAge}; SameSite=Strict${secure}`;
}

// [COMMENT]: Xóa cookie workspace_id ngay lập tức
function clearCookieWorkspaceID(): void {
  if (typeof document === "undefined") return;
  document.cookie = `${COOKIE_KEY}=; path=/; max-age=0; SameSite=Strict`;
}

// ─── Types ────────────────────────────────────────────────────────────────────

export type WorkspaceContextValue = {
  // Trạng thái
  catalog: WorkspaceCatalogItem[];
  activeWorkspaceID: string | null;
  loading: boolean;
  error: string | null;

  // Actions
  // [COMMENT]: Gọi sau login hoặc khi context thay đổi (zone / tenant switch)
  initWorkspaceContext: () => Promise<void>;
  // [COMMENT]: Chọn workspace cụ thể từ catalog — ghi đè cookie
  selectWorkspace: (id: string) => void;
  // [COMMENT]: Clear toàn bộ workspace state khi logout
  clearWorkspaceContext: () => void;
  // [COMMENT]: Thêm trực tiếp workspace mới tạo vào catalog dropdown trên client mà không cần gọi API (0-Request)
  addWorkspaceToCatalog: (item: WorkspaceCatalogItem) => void;
  // [COMMENT]: Xoá trực tiếp workspace khỏi catalog dropdown trên client (0-Request)
  removeWorkspaceFromCatalog: (id: string) => void;
};

// ─── Context ──────────────────────────────────────────────────────────────────

const WorkspaceContext = createContext<WorkspaceContextValue | null>(null);

export function WorkspaceProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient();
  const [catalog, setCatalog] = useState<WorkspaceCatalogItem[]>([]);
  const [activeWorkspaceID, setActiveWorkspaceID] = useState<string | null>(
    () => getCookieWorkspaceID(),
  );
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // [COMMENT]: Ref để cancel inflight request khi context thay đổi nhanh (zone/tenant switch race)
  const abortRef = useRef<AbortController | null>(null);

  // [COMMENT]: initWorkspaceContext — gọi sau khi login thành công hoặc khi switch zone/tenant.
  // Luôn clear cookie trước để đảm bảo workspace cũ không leak sang context mới.
  const initWorkspaceContext = useCallback(
    async () => {
      // Cancel request cũ nếu đang chạy
      abortRef.current?.abort();
      void queryClient.cancelQueries();
      const controller = new AbortController();
      abortRef.current = controller;

      // [COMMENT]: Clear cookie ngay lập tức vì zone hoặc tenant thay đổi → workspace cũ vô nghĩa
      clearCookieWorkspaceID();
      setActiveWorkspaceID(null);
      setLoading(true);
      setError(null);

      try {
        const items = await fetchWorkspaceCatalog(controller.signal);

        if (controller.signal.aborted || abortRef.current !== controller) return;

        setCatalog(items);

        if (items.length === 0) {
          // [COMMENT]: Không có workspace trong context này — để client redirect đến create workspace
          setActiveWorkspaceID(null);
          return;
        }

        // [COMMENT]: Auto-select workspace đầu tiên (ORDER BY created_at ASC từ server)
        const firstID = items[0].id;
        setCookieWorkspaceID(firstID);
        setActiveWorkspaceID(firstID);
      } catch (err: unknown) {
        // [COMMENT]: Bỏ qua lỗi abort — không phải lỗi thật
        if (controller.signal.aborted || abortRef.current !== controller || (err instanceof Error && err.name === "AbortError")) return;
        setError(err instanceof Error ? err.message : "Failed to load workspace catalog");
      } finally {
        if (abortRef.current === controller) {
          abortRef.current = null;
          setLoading(false);
        }
      }
    },
    [queryClient],
  );

  // [COMMENT]: selectWorkspace — user chủ động chọn workspace từ dropdown
  const selectWorkspace = useCallback((id: string) => {
    if (!catalog.some((workspace) => workspace.id === id)) return;
    void queryClient.cancelQueries();
    setCookieWorkspaceID(id);
    setActiveWorkspaceID(id);
  }, [catalog, queryClient]);

  // [COMMENT]: clearWorkspaceContext — dọn dẹp toàn bộ khi logout
  const clearWorkspaceContext = useCallback(() => {
    abortRef.current?.abort();
    clearCookieWorkspaceID();
    setCatalog([]);
    setActiveWorkspaceID(null);
    setError(null);
    setLoading(false);
  }, []);

  // [COMMENT]: addWorkspaceToCatalog — append trực tiếp workspace mới tạo vào catalog dropdown hiện tại trên client
  const addWorkspaceToCatalog = useCallback((item: WorkspaceCatalogItem) => {
    setCatalog((prev) => {
      // Tránh append trùng lặp nếu đã tồn tại
      if (prev.some((x) => x.id === item.id)) return prev;

      const newCatalog = [...prev, item];

      // Nếu hiện tại chưa chọn workspace nào trong context này, auto-select cái mới luôn
      const currentCookie = getCookieWorkspaceID();
      if (!currentCookie) {
        setCookieWorkspaceID(item.id);
        setActiveWorkspaceID(item.id);
      }

      return newCatalog;
    });
  }, []);

  // [COMMENT]: removeWorkspaceFromCatalog — xoá trực tiếp workspace đã xoá khỏi catalog dropdown trên client (0-Request)
  const removeWorkspaceFromCatalog = useCallback((id: string) => {
    setCatalog((prev) => {
      const newCatalog = prev.filter((x) => x.id !== id);

      // Nếu workspace bị xoá đang là active workspace, ta cần xoá active state và cookie
      const currentCookie = getCookieWorkspaceID();
      if (currentCookie === id) {
        clearCookieWorkspaceID();
        if (newCatalog.length > 0) {
          // Auto-select workspace tiếp theo còn lại
          const firstID = newCatalog[0].id;
          setCookieWorkspaceID(firstID);
          setActiveWorkspaceID(firstID);
        } else {
          setActiveWorkspaceID(null);
        }
      }

      return newCatalog;
    });
  }, []);

  // [COMMENT]: Cleanup khi unmount để cancel inflight request
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
    };
  }, []);

  const value: WorkspaceContextValue = {
    catalog,
    activeWorkspaceID,
    loading,
    error,
    initWorkspaceContext,
    selectWorkspace,
    clearWorkspaceContext,
    addWorkspaceToCatalog,
    removeWorkspaceFromCatalog,
  };

  return (
    <WorkspaceContext.Provider value={value}>
      {children}
    </WorkspaceContext.Provider>
  );
}

// ─── Hook ─────────────────────────────────────────────────────────────────────

// [COMMENT]: useWorkspace — hook tiêu thụ WorkspaceContext, throw nếu dùng ngoài Provider
export function useWorkspace(): WorkspaceContextValue {
  const ctx = useContext(WorkspaceContext);
  if (!ctx) {
    throw new Error("useWorkspace must be used within a <WorkspaceProvider>");
  }
  return ctx;
}
