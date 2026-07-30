
const DB_NAME = 'admin_device_crypto_v1'
const STORE_NAME = 'keys'
const KEY_PAIR_ID = 'device_keypair'

export function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte)
  })
  return btoa(binary)
}

export function base64ToBytes(base64: string): Uint8Array {
  const binaryString = atob(base64)
  const len = binaryString.length
  const bytes = new Uint8Array(len)
  for (let i = 0; i < len; i++) {
    bytes[i] = binaryString.charCodeAt(i)
  }
  return bytes
}

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, 1)
    request.onerror = () => reject(request.error)
    request.onsuccess = () => resolve(request.result)
    request.onupgradeneeded = () => {
      const db = request.result
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME)
      }
    }
  })
}

async function saveKeypair(publicKey: string, privateKey: CryptoKey): Promise<void> {
  const db = await openDB()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite')
    const store = tx.objectStore(STORE_NAME)
    const request = store.put({ publicKey, privateKey }, KEY_PAIR_ID)
    request.onerror = () => reject(request.error)
    request.onsuccess = () => resolve()
  })
}

async function loadKeypair(): Promise<{ publicKey: string; privateKey: CryptoKey } | null> {
  const db = await openDB()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readonly')
    const store = tx.objectStore(STORE_NAME)
    const request = store.get(KEY_PAIR_ID)
    request.onerror = () => reject(request.error)
    request.onsuccess = () => {
      if (request.result) {
        resolve(request.result)
      } else {
        resolve(null)
      }
    }
  })
}

export async function getOrCreateDeviceKeys(): Promise<{ publicKey: string; privateKey: CryptoKey }> {
  if (typeof window === 'undefined' || !window.indexedDB) {
    throw new Error('IndexedDB is not available in this environment.')
  }

  const existing = await loadKeypair()
  if (existing) {
    return existing
  }

  if (!crypto?.subtle) {
    throw new Error('WebCrypto is unavailable in this browser.')
  }

  // Generate a premium non-extractable Ed25519 keypair
  const keyPair = await crypto.subtle.generateKey(
    { name: 'Ed25519' },
    false, // extractable = false (Javascript cannot read the private key bytes ever!)
    ['sign', 'verify'],
  )

  const rawPublicKey = await crypto.subtle.exportKey('raw', keyPair.publicKey)
  const encodedPub = bytesToBase64(new Uint8Array(rawPublicKey))

  await saveKeypair(encodedPub, keyPair.privateKey)

  return { publicKey: encodedPub, privateKey: keyPair.privateKey }
}

export function generateNonce(): string {
  // [COMMENT]: ACR persists the signed nonce only as short-lived replay state.
  // UUID keeps the downstream proof vocabulary bounded without exposing it.
  return crypto.randomUUID()
}

export async function sha256Hex(text: string): Promise<string> {
  const encoder = new TextEncoder()
  const data = encoder.encode(text)
  const hashBuffer = await crypto.subtle.digest('SHA-256', data)
  const hashArray = Array.from(new Uint8Array(hashBuffer))
  return hashArray.map((b) => b.toString(16).padStart(2, '0')).join('')
}

export async function signPayload(payload: string, privateKey: CryptoKey): Promise<string> {
  if (!crypto?.subtle) {
    throw new Error('WebCrypto is unavailable in this browser.')
  }

  const encoder = new TextEncoder()
  const data = encoder.encode(payload)
  const signatureArrayBuffer = await crypto.subtle.sign(
    { name: 'Ed25519' },
    privateKey,
    data
  )

  return bytesToBase64(new Uint8Array(signatureArrayBuffer))
}
