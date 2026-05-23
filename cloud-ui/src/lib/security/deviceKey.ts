const STORAGE_KEY_HANDLE = "iam.device.key.v1";

type StoredKeyHandle = {
  publicKeyBase64: string;
  privateKeyJwk: JsonWebKey;
  publicKeyJwk: JsonWebKey;
};

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

function readStoredHandle(): StoredKeyHandle | null {
  if (typeof window === "undefined") {
    return null;
  }
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY_HANDLE);
    if (!raw) {
      return null;
    }
    const parsed = JSON.parse(raw) as StoredKeyHandle;
    if (!parsed?.publicKeyBase64 || !parsed?.privateKeyJwk) {
      return null;
    }
    return parsed;
  } catch {
    return null;
  }
}

function writeStoredHandle(handle: StoredKeyHandle): void {
  if (typeof window === "undefined") {
    return;
  }
  window.localStorage.setItem(STORAGE_KEY_HANDLE, JSON.stringify(handle));
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

export async function ensureDevicePublicKey(): Promise<string> {
  const existing = readStoredHandle();
  if (existing) {
    return existing.publicKeyBase64;
  }
  const created = await generateEd25519Keypair();
  writeStoredHandle(created);
  return created.publicKeyBase64;
}
