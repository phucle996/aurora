import { create } from 'zustand';
import { request } from '../api/fetcher';
import { ensureDevicePublicKey } from '../security/deviceKey';

export interface UserProfile {
  id: string;
  username: string;
}

interface BillingSessionResponse {
  authenticated: boolean;
  user: UserProfile;
  zone_id: string;
}

interface AuthState {
  isAuthenticated: boolean;
  user: UserProfile | null;
  isLoading: boolean;
  error: string | null;
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

  logout: async () => {
    set({ isLoading: true });
    try {
      await request<void>('/billing/auth/logout', { method: 'POST' });
    } catch {
      // [COMMENT]: Logout local vẫn hoàn tất khi edge đang unavailable; cookie có TTL độc lập.
    } finally {
      sessionStorage.removeItem('billing.pkce.verifier');
      sessionStorage.removeItem('billing.pkce.state');
      set({ isAuthenticated: false, user: null, isLoading: false, error: null });
    }
  },

  checkSession: async () => {
    set({ isLoading: true, error: null });
    try {
      if (window.location.pathname === '/auth/start') {
        sessionStorage.setItem('billing.authorization.redirecting', '1');
        const randomBase64URL = (byteLength: number) => {
          const bytes = window.crypto.getRandomValues(new Uint8Array(byteLength));
          let binary = '';
          for (const byte of bytes) binary += String.fromCharCode(byte);
          return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
        };
        const verifier = randomBase64URL(48);
        const verifierDigest = await window.crypto.subtle.digest(
          'SHA-256',
          new TextEncoder().encode(verifier),
        );
        let challengeBinary = '';
        for (const byte of new Uint8Array(verifierDigest)) challengeBinary += String.fromCharCode(byte);
        const challenge = btoa(challengeBinary)
          .replace(/\+/g, '-')
          .replace(/\//g, '_')
          .replace(/=+$/g, '');
        const state = randomBase64URL(32);

        // [COMMENT]: Verifier chỉ sống trong Cost origin; Cloud và ACR chỉ nhìn thấy SHA-256 challenge.
        sessionStorage.setItem('billing.pkce.verifier', verifier);
        sessionStorage.setItem('billing.pkce.state', state);
        const cloudOrigin = import.meta.env.VITE_CLOUD_CONSOLE_URL || 'https://cloud.aurora.local';
        window.location.replace(
          `${cloudOrigin}/billing/authorize?state=${encodeURIComponent(state)}&code_challenge=${encodeURIComponent(challenge)}`,
        );
        return;
      }

      if (window.location.pathname === '/auth/handoff') {
        const fragment = new URLSearchParams(window.location.hash.replace(/^#/, ''));
        const handoffCode = fragment.get('code');
        const returnedState = fragment.get('state');
        const expectedState = sessionStorage.getItem('billing.pkce.state');
        const verifier = sessionStorage.getItem('billing.pkce.verifier');
        // [COMMENT]: Xóa code khỏi address bar trước network để extension/screenshot không giữ credential.
        window.history.replaceState({}, document.title, '/');
        if (!handoffCode || !/^[0-9a-f]{64}$/.test(handoffCode)) {
          throw new Error('Billing handoff code is missing or invalid');
        }
        if (!returnedState || !expectedState || returnedState !== expectedState || !verifier) {
          throw new Error('Billing authorization state is missing or invalid');
        }
        await request<void>('/billing/auth/exchange', {
          method: 'POST',
          body: JSON.stringify({
            handoff_code: handoffCode,
            code_verifier: verifier,
            device_public_key: await ensureDevicePublicKey(),
          }),
        });
        sessionStorage.removeItem('billing.pkce.verifier');
        sessionStorage.removeItem('billing.pkce.state');
        sessionStorage.removeItem('billing.authorization.redirecting');
      }

      const session = await request<BillingSessionResponse>('/billing/auth/session', { method: 'GET' });
      if (!session.authenticated) throw new Error('Billing session is not authenticated');
      sessionStorage.removeItem('billing.authorization.redirecting');
      set({
        isAuthenticated: true,
        user: session.user,
        isLoading: false,
        error: null,
      });
    } catch (error) {
      if (
        window.location.pathname !== '/auth/start' &&
        window.location.pathname !== '/auth/handoff' &&
        sessionStorage.getItem('billing.authorization.redirecting') !== '1'
      ) {
        // [COMMENT]: Alias mất/hết hạn tự khởi động PKCE đúng một lần; guard ngăn redirect loop khi Cloud IAM cũng đã hết hạn.
        sessionStorage.setItem('billing.authorization.redirecting', '1');
        window.location.replace('/auth/start');
        return;
      }
      set({
        isAuthenticated: false,
        user: null,
        isLoading: false,
        error: error instanceof Error ? error.message : 'Billing session is unavailable',
      });
    }
  },
}));
