import { create } from 'zustand';
import { request } from '../api/fetcher';

export interface UserProfile {
  username: string;
  fullname: string;
  email: string;
}

interface AuthState {
  isAuthenticated: boolean;
  user: UserProfile | null;
  isLoading: boolean;
  error: string | null;
  login: (employeeCode: string, secretKey: string) => Promise<boolean>;
  logout: () => Promise<void>;
  checkSession: () => Promise<void>;
  clearError: () => void;
}

export const useAuthStore = create<AuthState>((set) => ({
  isAuthenticated: false,
  user: null,
  isLoading: false,
  error: null,

  clearError: () => set({ error: null }),

  login: async (employeeCode, secretKey) => {
    set({ isLoading: true, error: null });
    try {
      // 1. Thực hiện gọi API đăng nhập đi kèm credentials để nhận HttpOnly session cookie
      await request<void>('/billing/auth/login', {
        method: 'POST',
        body: JSON.stringify({
          employee_code: employeeCode,
          secret_key: secretKey,
          trust_device: true,
          zone_code: 'edge-viet-nam-1',
        }),
      });

      // 2. Lấy profile chi tiết từ API /me/iam/profile/read qua fetcher
      const profile = await request<UserProfile>('/me/iam/profile/read', {
        method: 'GET',
      });

      // 3. Đánh dấu đăng nhập thành công vào store và localStorage làm flag phụ
      localStorage.setItem('cost_console_logged_in', 'true');
      set({
        isAuthenticated: true,
        user: profile,
        isLoading: false,
        error: null,
      });
      return true;
    } catch (err: any) {
      set({
        isLoading: false,
        error: err.message || 'Đã xảy ra lỗi không xác định',
        isAuthenticated: false,
        user: null,
      });
      return false;
    }
  },

  logout: async () => {
    set({ isLoading: true });
    try {
      await request<void>('/billing/auth/logout', {
        method: 'POST',
      });
    } catch {
      // Best-effort logout
    } finally {
      localStorage.removeItem('cost_console_logged_in');
      set({
        isAuthenticated: false,
        user: null,
        isLoading: false,
        error: null,
      });
    }
  },

  checkSession: async () => {
    // Chỉ check khi có flag trong localStorage để tránh spam request 401 lúc chưa đăng nhập
    if (localStorage.getItem('cost_console_logged_in') !== 'true') {
      return;
    }
    set({ isLoading: true });
    try {
      const profile = await request<UserProfile>('/me/iam/profile/read', {
        method: 'GET',
      });
      set({
        isAuthenticated: true,
        user: profile,
        isLoading: false,
      });
    } catch {
      // Offline hoặc lỗi mạng/hết hạn, làm sạch local state
      localStorage.removeItem('cost_console_logged_in');
      set({
        isAuthenticated: false,
        user: null,
        isLoading: false,
      });
    }
  },
}));
