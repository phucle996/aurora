const DB_NAME = 'billing-security-db';
const STORE_NAME = 'session-proof-keys';
const KEY_NAME = 'billing.session-proof.key.v1';

type StoredKey = {
  publicKeyBase64: string;
  privateKey: CryptoKey;
};

function openDatabase(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1);
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(STORE_NAME)) {
        request.result.createObjectStore(STORE_NAME);
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function base64URLToBytes(value: string): Uint8Array {
  const normalized = value.replace(/-/g, '+').replace(/_/g, '/');
  const binary = atob(normalized + '='.repeat((4 - normalized.length % 4) % 4));
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

async function readStoredKey(): Promise<StoredKey | null> {
  const database = await openDatabase();
  return new Promise((resolve, reject) => {
    const request = database.transaction(STORE_NAME, 'readonly').objectStore(STORE_NAME).get(KEY_NAME);
    request.onsuccess = () => resolve(request.result || null);
    request.onerror = () => reject(request.error);
  });
}

async function createStoredKey(): Promise<StoredKey> {
  const pair = await window.crypto.subtle.generateKey(
    { name: 'Ed25519' } as AlgorithmIdentifier,
    true,
    ['sign', 'verify'],
  ) as CryptoKeyPair;
  const publicJWK = await window.crypto.subtle.exportKey('jwk', pair.publicKey);
  if (!publicJWK.x) throw new Error('Browser did not produce an Ed25519 public key');
  const publicBytes = base64URLToBytes(publicJWK.x);
  if (publicBytes.length !== 32) throw new Error('Browser produced an invalid Ed25519 public key');

  // [COMMENT]: Re-import private key non-extractable trước khi persist vào IndexedDB của Cost origin.
  const privateJWK = await window.crypto.subtle.exportKey('jwk', pair.privateKey);
  const privateKey = await window.crypto.subtle.importKey(
    'jwk',
    privateJWK,
    { name: 'Ed25519' } as AlgorithmIdentifier,
    false,
    ['sign'],
  );
  const stored = { publicKeyBase64: bytesToBase64(publicBytes), privateKey };
  const database = await openDatabase();
  await new Promise<void>((resolve, reject) => {
    const request = database.transaction(STORE_NAME, 'readwrite').objectStore(STORE_NAME).put(stored, KEY_NAME);
    request.onsuccess = () => resolve();
    request.onerror = () => reject(request.error);
  });
  return stored;
}

async function signingKey(): Promise<StoredKey> {
  if (!window.crypto?.subtle || !window.indexedDB) {
    throw new Error('This browser does not support secure session proof keys');
  }
  return (await readStoredKey()) ?? createStoredKey();
}

export async function ensureDevicePublicKey(): Promise<string> {
  return (await signingKey()).publicKeyBase64;
}

export async function signSessionProof(message: string): Promise<string> {
  const key = await signingKey();
  const signature = await window.crypto.subtle.sign(
    { name: 'Ed25519' } as AlgorithmIdentifier,
    key.privateKey,
    new TextEncoder().encode(message),
  );
  return bytesToBase64(new Uint8Array(signature));
}
