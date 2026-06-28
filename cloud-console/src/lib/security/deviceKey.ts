const DB_NAME = "iam-security-db";
const STORE_NAME = "device-keys";
const KEY_NAME = "iam.device.key.v1";

type StoredKeyHandle = {
  publicKeyBase64: string;
  privateKeyJwk: JsonWebKey;
  publicKeyJwk: JsonWebKey;
};

// [COMMENT]: Khởi tạo hoặc kết nối cơ sở dữ liệu IndexedDB của trình duyệt
function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME);
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

// [COMMENT]: Đọc thông tin khóa bảo mật được lưu trữ an toàn trong IndexedDB
async function getStoredHandleIndexedDB(): Promise<StoredKeyHandle | null> {
  if (typeof window === "undefined") return null;
  try {
    const db = await openDB();
    return new Promise((resolve, reject) => {
      const transaction = db.transaction(STORE_NAME, "readonly");
      const store = transaction.objectStore(STORE_NAME);
      const request = store.get(KEY_NAME);
      request.onsuccess = () => resolve(request.result || null);
      request.onerror = () => reject(request.error);
    });
  } catch (err) {
    console.error("[IndexedDB] Error reading stored handle", err);
    return null;
  }
}

// [COMMENT]: Ghi thông tin khóa bảo mật vào IndexedDB
async function setStoredHandleIndexedDB(handle: StoredKeyHandle): Promise<void> {
  if (typeof window === "undefined") return;
  try {
    const db = await openDB();
    return new Promise((resolve, reject) => {
      const transaction = db.transaction(STORE_NAME, "readwrite");
      const store = transaction.objectStore(STORE_NAME);
      const request = store.put(handle, KEY_NAME);
      request.onsuccess = () => resolve();
      request.onerror = () => reject(request.error);
    });
  } catch (err) {
    console.error("[IndexedDB] Error writing stored handle", err);
  }
}

function isWebCryptoAvailable(): boolean {
  return typeof window !== "undefined" && !!window.crypto?.subtle;
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  for (const b of bytes) {
    binary += String.fromCharCode(b);
  }
  return btoa(binary);
}

function base64UrlDecode(value: string): Uint8Array {
  const padded = value.replace(/-/g, "+").replace(/_/g, "/");
  const padding =
    padded.length % 4 === 0 ? "" : "=".repeat(4 - (padded.length % 4));
  const binary = atob(padded + padding);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    out[i] = binary.charCodeAt(i);
  }
  return out;
}

async function generateEd25519Keypair(): Promise<StoredKeyHandle> {
  if (!isWebCryptoAvailable()) {
    throw new DeviceKeyUnsupportedError();
  }
  const subtle = window.crypto.subtle;
  let keyPair: CryptoKeyPair;
  try {
    keyPair = (await subtle.generateKey(
      { name: "Ed25519" } as AlgorithmIdentifier,
      true,
      ["sign", "verify"],
    )) as CryptoKeyPair;
  } catch {
    throw new DeviceKeyUnsupportedError();
  }

  const publicKeyJwk = await subtle.exportKey("jwk", keyPair.publicKey);
  const privateKeyJwk = await subtle.exportKey("jwk", keyPair.privateKey);
  if (!publicKeyJwk.x) {
    throw new DeviceKeyUnsupportedError();
  }

  const publicKeyBytes = base64UrlDecode(publicKeyJwk.x);
  if (publicKeyBytes.length !== 32) {
    throw new DeviceKeyUnsupportedError();
  }

  return {
    publicKeyBase64: bytesToBase64(publicKeyBytes),
    publicKeyJwk,
    privateKeyJwk,
  };
}

export class DeviceKeyUnsupportedError extends Error {
  constructor() {
    super("device_key_unsupported");
    this.name = "DeviceKeyUnsupportedError";
  }
}

// [COMMENT]: Lấy hoặc khởi tạo khóa công khai của thiết bị từ IndexedDB (lưu trữ độc quyền, an toàn và chống XSS).
export async function ensureDevicePublicKey(): Promise<string> {
  // 1. Đọc khóa từ IndexedDB
  const existing = await getStoredHandleIndexedDB();
  if (existing) {
    return existing.publicKeyBase64;
  }

  // 2. Nếu chưa có cặp khóa nào, sinh mới và lưu trực tiếp vào IndexedDB
  const created = await generateEd25519Keypair();
  await setStoredHandleIndexedDB(created);
  return created.publicKeyBase64;
}
