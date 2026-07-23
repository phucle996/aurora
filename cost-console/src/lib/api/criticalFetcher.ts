import { request } from './fetcher';
import { signSessionProof } from '../security/deviceKey';

type CriticalChallenge = {
  challenge_id: string;
  nonce: string;
  expires_in: number;
};

type CriticalOptions = Omit<RequestInit, 'body' | 'method'> & {
  method: 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  body?: unknown;
};

async function sha256Hex(value: string): Promise<string> {
  const digest = await window.crypto.subtle.digest('SHA-256', new TextEncoder().encode(value));
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join('');
}

// [COMMENT]: Base duy nhất cho Billing mutation critical: mỗi call lấy nonce và ký đúng wire body một lần.
export async function criticalFetcher<T>(path: string, options: CriticalOptions): Promise<T> {
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  if (!normalizedPath.startsWith('/billing/critical/') || normalizedPath.includes('?')) {
    throw new Error('criticalFetcher only accepts query-free /billing/critical/* paths');
  }
  const challenge = await request<CriticalChallenge>('/billing/auth/session-proof/challenge', {
    method: 'POST',
  });
  const serializedBody = options.body === undefined ? '' : JSON.stringify(options.body);
  const timestamp = Math.floor(Date.now() / 1000);
  const fullPath = `/api/v1${normalizedPath}`;
  const message = [
    'aurora.session-proof.v1',
    challenge.challenge_id,
    challenge.nonce,
    options.method,
    fullPath,
    await sha256Hex(serializedBody),
    String(timestamp),
  ].join('\n');
  const headers = new Headers(options.headers);
  headers.set('x-session-proof-challenge-id', challenge.challenge_id);
  headers.set('x-session-proof-timestamp', String(timestamp));
  headers.set('x-session-proof-signature', await signSessionProof(message));
  return request<T>(normalizedPath, {
    ...options,
    headers,
    body: serializedBody,
  });
}
