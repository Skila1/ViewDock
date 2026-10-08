import { chunkAad, defaultSubtle, encryptChunk, generateVaultKey, sha256Hex } from "./crypto";
import type { VaultManifest } from "./manifest";

// Schema shared with web/public/sw.js, which opens the same database to serve
// offline playback. Keep VAULT_DB, VAULT_DB_VERSION and the store names in sync.
export const VAULT_DB = "viewdock-vault-v2";
export const VAULT_DB_VERSION = 1;
const KEYS = "keys";
const ITEMS = "items";
const CHUNKS = "chunks";

export type VaultStatus = "downloading" | "paused" | "error" | "quota" | "verifying" | "complete" | "corrupt";

export type VaultRecord = {
  key: string;
  userId: string;
  itemKind: "movie" | "episode";
  itemId: string;
  libraryId?: string;
  mediaFileId?: string;
  title: string;
  mime: string;
  size: number;
  chunkSize: number;
  etag?: string;
  durationMs?: number;
  status: VaultStatus;
  error?: string;
  // Plaintext SHA-256 per chunk; null until the chunk is stored.
  hashes: (string | null)[];
  bytesStored: number;
  manifest?: VaultManifest;
  createdAt: string;
  downloadedAt?: string;
  expiresAt?: string;
  positionMs?: number;
  positionAt?: string;
};

type KeyRow = { userId: string; key: CryptoKey; createdAt: string };

export function vaultKey(userId: string, itemKind: string, itemId: string): string {
  return `${userId}|${itemKind}|${itemId}`;
}

export function isQuotaError(error: unknown): boolean {
  if (!(error instanceof DOMException) && !(error instanceof Error)) return false;
  return error.name === "QuotaExceededError" || /quota/i.test(error.message);
}

let dbPromise: Promise<IDBDatabase> | null = null;

export function upgradeVaultSchema(db: IDBDatabase) {
  if (!db.objectStoreNames.contains(KEYS)) db.createObjectStore(KEYS, { keyPath: "userId" });
  if (!db.objectStoreNames.contains(ITEMS)) db.createObjectStore(ITEMS, { keyPath: "key" }).createIndex("userId", "userId");
  if (!db.objectStoreNames.contains(CHUNKS)) db.createObjectStore(CHUNKS);
}

export function openVaultDB(): Promise<IDBDatabase> {
  if (dbPromise) return dbPromise;
  dbPromise = new Promise<IDBDatabase>((resolve, reject) => {
    if (typeof indexedDB === "undefined") {
      reject(new Error("This browser does not provide IndexedDB, so offline titles cannot be stored."));
      return;
    }
    const request = indexedDB.open(VAULT_DB, VAULT_DB_VERSION);
    request.onupgradeneeded = () => upgradeVaultSchema(request.result);
    request.onsuccess = () => {
      const db = request.result;
      // Sign-out deletes the database from the service worker; release it.
      db.onversionchange = () => {
        db.close();
        dbPromise = null;
      };
      db.onclose = () => {
        dbPromise = null;
      };
      resolve(db);
    };
    request.onerror = () => reject(request.error ?? new Error("offline vault unavailable"));
    request.onblocked = () => reject(new Error("offline vault is being cleared in another tab; try again"));
  }).catch((error) => {
    dbPromise = null;
    throw error;
  });
  return dbPromise;
}

function done(tx: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error ?? new Error("offline vault write failed"));
    tx.onabort = () => reject(tx.error ?? new DOMException("offline vault write aborted", "AbortError"));
  });
}

function result<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error("offline vault read failed"));
  });
}

function chunkRange(key: string): IDBKeyRange {
  return IDBKeyRange.bound([key, 0], [key, Number.MAX_SAFE_INTEGER]);
}

/** userKey returns the account's vault key, creating it on first use. */
export async function userKey(userId: string, subtle: SubtleCrypto = defaultSubtle()): Promise<CryptoKey> {
  const db = await openVaultDB();
  const existing = await result<KeyRow | undefined>(db.transaction(KEYS).objectStore(KEYS).get(userId));
  if (existing?.key) return existing.key;
  const key = await generateVaultKey(subtle);
  const tx = db.transaction(KEYS, "readwrite");
  const add = tx.objectStore(KEYS).add({ userId, key, createdAt: new Date().toISOString() } satisfies KeyRow);
  // Another tab may have created the key first; the stored one wins.
  add.onerror = (event) => {
    if (add.error?.name === "ConstraintError") event.preventDefault();
  };
  try {
    await done(tx);
  } catch (error) {
    if (!(error instanceof DOMException && error.name === "ConstraintError")) throw error;
  }
  const stored = await result<KeyRow | undefined>(db.transaction(KEYS).objectStore(KEYS).get(userId));
  if (!stored?.key) throw new Error("offline vault key could not be stored");
  return stored.key;
}

export async function hasUserKey(userId: string): Promise<boolean> {
  const db = await openVaultDB();
  return Boolean(await result(db.transaction(KEYS).objectStore(KEYS).getKey(userId)));
}

export async function listRecords(userId: string): Promise<VaultRecord[]> {
  const db = await openVaultDB();
  const rows = await result<VaultRecord[]>(db.transaction(ITEMS).objectStore(ITEMS).index("userId").getAll(userId));
  return rows.sort((a, b) => (b.downloadedAt ?? b.createdAt).localeCompare(a.downloadedAt ?? a.createdAt));
}

export async function getRecord(key: string): Promise<VaultRecord | undefined> {
  const db = await openVaultDB();
  return result<VaultRecord | undefined>(db.transaction(ITEMS).objectStore(ITEMS).get(key));
}

export async function putRecord(record: VaultRecord): Promise<void> {
  const db = await openVaultDB();
  const tx = db.transaction(ITEMS, "readwrite");
  tx.objectStore(ITEMS).put(record);
  await done(tx);
}

export async function patchRecord(key: string, patch: Partial<VaultRecord>): Promise<VaultRecord | undefined> {
  const db = await openVaultDB();
  const tx = db.transaction(ITEMS, "readwrite");
  const store = tx.objectStore(ITEMS);
  const current = await result<VaultRecord | undefined>(store.get(key));
  let next: VaultRecord | undefined;
  if (current) {
    next = { ...current, ...patch, key };
    store.put(next);
  }
  await done(tx);
  return next;
}

/**
 * commitChunk encrypts one plaintext chunk and stores it together with its
 * digest in a single transaction, so a recorded digest always means the
 * chunk is present.
 */
export async function commitChunk(record: VaultRecord, index: number, plain: Uint8Array<ArrayBuffer>, key: CryptoKey, subtle: SubtleCrypto = defaultSubtle()): Promise<VaultRecord> {
  const [hash, sealed] = await Promise.all([sha256Hex(plain, subtle), encryptChunk(key, plain, chunkAad(record.key, index), subtle)]);
  const db = await openVaultDB();
  const tx = db.transaction([ITEMS, CHUNKS], "readwrite");
  const hashes = record.hashes.slice();
  const fresh = !hashes[index];
  hashes[index] = hash;
  const next: VaultRecord = { ...record, hashes, bytesStored: record.bytesStored + (fresh ? plain.byteLength : 0) };
  tx.objectStore(CHUNKS).put(sealed, [record.key, index]);
  tx.objectStore(ITEMS).put(next);
  await done(tx);
  return next;
}

export async function getChunk(key: string, index: number): Promise<ArrayBuffer | undefined> {
  const db = await openVaultDB();
  return result<ArrayBuffer | undefined>(db.transaction(CHUNKS).objectStore(CHUNKS).get([key, index]));
}

export async function storedChunkIndexes(key: string): Promise<Set<number>> {
  const db = await openVaultDB();
  const keys = await result(db.transaction(CHUNKS).objectStore(CHUNKS).getAllKeys(chunkRange(key)));
  return new Set(keys.map((k) => (k as [string, number])[1]));
}

/** clearChunks drops stored bytes but keeps the record, for restarts after the source changed. */
export async function clearChunks(key: string): Promise<void> {
  const db = await openVaultDB();
  const tx = db.transaction(CHUNKS, "readwrite");
  tx.objectStore(CHUNKS).delete(chunkRange(key));
  await done(tx);
}

export async function deleteRecord(key: string): Promise<void> {
  const db = await openVaultDB();
  const tx = db.transaction([ITEMS, CHUNKS], "readwrite");
  tx.objectStore(CHUNKS).delete(chunkRange(key));
  tx.objectStore(ITEMS).delete(key);
  await done(tx);
}

/** clearUserVault crypto-shreds an account's vault: key, records and chunks. */
export async function clearUserVault(userId: string): Promise<void> {
  const records = await listRecords(userId);
  const db = await openVaultDB();
  const tx = db.transaction([KEYS, ITEMS, CHUNKS], "readwrite");
  tx.objectStore(KEYS).delete(userId);
  for (const record of records) {
    tx.objectStore(CHUNKS).delete(chunkRange(record.key));
    tx.objectStore(ITEMS).delete(record.key);
  }
  await done(tx);
}

/** clearVault removes every account's vault on this device. */
export async function clearVault(): Promise<void> {
  if (typeof indexedDB === "undefined") return;
  const open = dbPromise;
  dbPromise = null;
  if (open) await open.then((db) => db.close()).catch(() => {});
  await new Promise<void>((resolve, reject) => {
    const request = indexedDB.deleteDatabase(VAULT_DB);
    request.onsuccess = () => resolve();
    request.onerror = () => reject(request.error ?? new Error("offline vault could not be cleared"));
  });
}
