import { create } from 'zustand';
import { request } from '../api/fetcher';
import { queryClient } from '../queryClient';
import { ensureDevicePublicKey } from '../security/deviceKey';
import { getRenderContext, type RenderContext } from '../../session/render-context';

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
  renderContext: RenderContext | null;
  isLoading: boolean;
  error: string | null;
  logout: () => Promise<void>;
  checkSession: () => Promise<void>;
  clearError: () => void;
  checkPermission: (key: string, action: string) => boolean;
}

let sessionAttempt = 0;
let sessionAbort: AbortController | null = null;

export const useAuthStore = create<AuthState>((set, get) => ({
  isAuthenticated: false,
  user: null,
  renderContext: null,
  isLoading: false,
  error: null,

  clearError: () => set({ error: null }),
  checkPermission: (key, action) => {
    const { isAuthenticated, renderContext } = get();
    if (!isAuthenticated || !renderContext) return false;
    const expected = key.split(':');
    if (expected.length !== 2) return false;

    return renderContext.navigation.some((entry) => {
      const actual = entry.key.split(':');
      return actual.length === 2
        && expected.every((part, index) => part === '*' || part === actual[index])
        && entry.actions.includes(action);
    });
  },

  logout: async () => {
    sessionAttempt += 1;
    sessionAbort?.abort();
    set({ isLoading: true });
    try {
      await request<void>('/billing/auth/logout', { method: 'POST' });
    } catch {
      // [COMMENT]: Logout local vẫn hoàn tất khi edge đang unavailable; cookie có TTL độc lập.
    } finally {
      sessionStorage.removeItem('billing.pkce.verifier');
      sessionStorage.removeItem('billing.pkce.state');
      sessionStorage.removeItem('billing.authorization.redirecting');
      queryClient.clear();
      set({
        isAuthenticated: false,
        user: null,
        renderContext: null,
        isLoading: false,
        error: null,
      });
    }
  },

  checkSession: async () => {
    const attempt = sessionAttempt + 1;
    sessionAttempt = attempt;
    sessionAbort?.abort();
    const controller = new AbortController();
    sessionAbort = controller;
    const previousUserID = get().user?.id;
    // Session verification is a principal boundary. Unmount protected UI while
    // both the alias and IAM Render Context are being refreshed.
    set({
      isAuthenticated: false,
      user: null,
      renderContext: null,
      isLoading: true,
      error: null,
    });
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
        // [DEV-ONLY]: compose injects https://localhost so a Cost Console
        // login cannot start OAuth on cloud.aurora.local and lose host-only
        // cookies when Google returns to localhost. Production must inject
        // the verified Cloud Console origin.
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
          signal: controller.signal,
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

      const [session, renderContext] = await Promise.all([
        request<BillingSessionResponse>('/billing/auth/session', {
          method: 'GET',
          signal: controller.signal,
        }),
        getRenderContext(controller.signal),
      ]);
      if (!session.authenticated) throw new Error('Billing session is not authenticated');
      if (controller.signal.aborted || attempt !== sessionAttempt) return;

      sessionStorage.removeItem('billing.authorization.redirecting');
      if (previousUserID && previousUserID !== session.user.id) {
        // An opaque alias may point to a different IAM principal after logout/login.
        // Clear every completed query before publishing the new render context.
        queryClient.clear();
      }
      set({
        isAuthenticated: true,
        user: session.user,
        renderContext,
        isLoading: false,
        error: null,
      });
    } catch (error) {
      if (attempt !== sessionAttempt || controller.signal.aborted) return;
      queryClient.clear();
      set({
        isAuthenticated: false,
        user: null,
        renderContext: null,
        isLoading: false,
        error: error instanceof Error ? error.message : 'Billing session is unavailable',
      });
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
    } finally {
      if (sessionAbort === controller) sessionAbort = null;
    }
  },
}));
