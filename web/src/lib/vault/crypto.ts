// AES-GCM chunk encryption for the Offline Vault. Stored chunks are laid out
// as iv (12 bytes) followed by ciphertext and tag. The additional data binds a
// chunk to its item and position, so chunks cannot be swapped or replayed
// between items without failing authentication.

export const IV_BYTES = 12;
export const TAG_BYTES = 16;
export const CHUNK_OVERHEAD = IV_BYTES + TAG_BYTES;

export function defaultSubtle(): SubtleCrypto {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) throw new Error("This browser does not provide WebCrypto, which the Offline Vault requires. A secure (HTTPS) connection is needed.");
  return subtle;
}

function randomBytes(length: number): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(length);
  globalThis.crypto.getRandomValues(out);
  return out;
}

const encoder = new TextEncoder();

export function chunkAad(itemKey: string, index: number): Uint8Array<ArrayBuffer> {
  return encoder.encode(`viewdock-vault:${itemKey}#${index}`);
}

/** generateVaultKey creates a non-extractable AES-GCM-256 key. */
export function generateVaultKey(subtle: SubtleCrypto = defaultSubtle()): Promise<CryptoKey> {
  return subtle.generateKey({ name: "AES-GCM", length: 256 }, false, ["encrypt", "decrypt"]) as Promise<CryptoKey>;
}

export async function encryptChunk(key: CryptoKey, plain: BufferSource, aad: Uint8Array<ArrayBuffer>, subtle: SubtleCrypto = defaultSubtle()): Promise<ArrayBuffer> {
  const iv = randomBytes(IV_BYTES);
  const sealed = new Uint8Array(await subtle.encrypt({ name: "AES-GCM", iv, additionalData: aad }, key, plain));
  const out = new Uint8Array(IV_BYTES + sealed.byteLength);
  out.set(iv, 0);
  out.set(sealed, IV_BYTES);
  return out.buffer;
}

/** decryptChunk throws when the chunk was altered, truncated or moved. */
export async function decryptChunk(key: CryptoKey, stored: ArrayBuffer, aad: Uint8Array<ArrayBuffer>, subtle: SubtleCrypto = defaultSubtle()): Promise<ArrayBuffer> {
  if (stored.byteLength < CHUNK_OVERHEAD) throw new Error("stored chunk is truncated");
  const iv = new Uint8Array(stored, 0, IV_BYTES);
  const body = new Uint8Array(stored, IV_BYTES);
  return subtle.decrypt({ name: "AES-GCM", iv, additionalData: aad }, key, body);
}

export function toHex(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let out = "";
  for (let i = 0; i < bytes.length; i++) out += bytes[i].toString(16).padStart(2, "0");
  return out;
}

export async function sha256Hex(data: BufferSource | string, subtle: SubtleCrypto = defaultSubtle()): Promise<string> {
  const input = typeof data === "string" ? encoder.encode(data) : data;
  return toHex(await subtle.digest("SHA-256", input));
}
