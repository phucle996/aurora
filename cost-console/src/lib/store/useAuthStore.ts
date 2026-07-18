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

      // [COMMENT]: Chỉ đánh dấu đăng nhập thành công vào store và localStorage, profile sẽ hiển thị fallback hoặc load qua session check sau.
      localStorage.setItem('cost_console_logged_in', 'true');
      set({
        isAuthenticated: true,
        user: {
          username: employeeCode,
          fullname: 'Kế toán trưởng',
          email: 'finance@aurora.cloud',
        },
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
      // [COMMENT]: Gọi /billing/auth/session để biết trạng thái đăng nhập thay vì truy vấn profile bên IAM
      const res = await request<{ authenticated: boolean; user?: UserProfile }>('/billing/auth/session', {
        method: 'GET',
      });
      if (res.authenticated) {
        set({
          isAuthenticated: true,
          user: res.user || {
            username: 'accountant',
            fullname: 'Kế toán trưởng',
            email: 'finance@aurora.cloud',
          },
          isLoading: false,
        });
      } else {
        throw new Error('Unauthenticated');
      }
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
