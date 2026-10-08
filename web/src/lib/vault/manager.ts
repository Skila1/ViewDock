import { api, sessionPath } from "@/api/api";
import { DEFAULT_OFFLINE_POLICY, forgetCachedPolicy, type OfflinePolicy } from "@/api/vault";
import { listVault as listLegacyVault, removeVaultItem as removeLegacyItem } from "@/lib/offlineVault";
import { chunkAad, decryptChunk, sha256Hex } from "./crypto";
import {
  clearChunks,
  clearVault,
  commitChunk,
  deleteRecord,
  getChunk,
  getRecord,
  hasUserKey,
  isQuotaError,
  listRecords,
  patchRecord,
  putRecord,
  storedChunkIndexes,
  userKey,
  vaultKey,
  type VaultRecord,
} from "./db";
import { probe, transfer, VaultError, type ProbeResult } from "./downloader";
import { buildManifest, verifyManifest } from "./manifest";
import { checkAdmission, expiryFor, planPolicy } from "./policy";
import { chunkCount, chunkLength, DEFAULT_CHUNK_SIZE } from "./range";

function storedBytes(hashes: readonly (string | null)[], size: number, chunkSize: number): number {
  let total = 0;
  for (let index = 0; index < hashes.length; index++) if (hashes[index]) total += chunkLength(index, size, chunkSize);
  return total;
}

export type VaultTitle = { kind: "movie" | "episode"; id: string; title: string; libraryId?: string; durationMs?: number };

export type PreparedDownload = ProbeResult & {
  userId: string;
  title: VaultTitle;
  sessionId: string;
  mediaFileId?: string;
  durationMs?: number;
  estimate: StorageEstimate;
  persisted: boolean;
};

export type VaultEvent = { type: "update"; record: VaultRecord } | { type: "remove"; key: string } | { type: "refresh" };

export type ReconcileReport = { expired: number; revoked: number; evicted: number; unreadable: number; migrated: number };

const MAX_ACTIVE = 2;
const LEGACY_VAULT_DB = "viewdock-vault";
const VERIFY_ROUNDS = 2;
export const VAULT_MEDIA_PREFIX = "/vault-media/";

type Active = { controller: AbortController; canceled: boolean; done: Promise<void> };

const active = new Map<string, Active>();
const waiting: { userId: string; key: string; sessionId?: string }[] = [];
const listeners = new Set<(event: VaultEvent) => void>();
let policy: OfflinePolicy = DEFAULT_OFFLINE_POLICY;

function emit(event: VaultEvent) {
  for (const listener of listeners) listener(event);
}

export function subscribeVault(listener: (event: VaultEvent) => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function setVaultPolicy(next: OfflinePolicy) {
  policy = next;
}

export function isDownloading(key: string): boolean {
  return active.has(key);
}

export function isWaiting(key: string): boolean {
  return waiting.some((w) => w.key === key);
}

export function vaultMediaUrl(key: string): string {
  return `${VAULT_MEDIA_PREFIX}${encodeURIComponent(key)}`;
}

export async function storageEstimate(): Promise<StorageEstimate> {
  try {
    return (await navigator.storage?.estimate?.()) ?? {};
  } catch {
    return {};
  }
}

export async function storagePersisted(): Promise<boolean> {
  try {
    return (await navigator.storage?.persisted?.()) ?? false;
  } catch {
    return false;
  }
}

export async function requestPersistence(): Promise<boolean> {
  try {
    if (await storagePersisted()) return true;
    return (await navigator.storage?.persist?.()) ?? false;
  } catch {
    return false;
  }
}

function message(error: unknown): string {
  if (error instanceof VaultError) return error.message;
  if (isQuotaError(error)) return "Browser storage is full. Remove offline titles or free up disk space, then resume.";
  if (error instanceof Error && error.message) return error.message;
  return "Offline download failed.";
}

async function openSession(title: VaultTitle, mediaFileId?: string) {
  const session = await api.createSession({ item_kind: title.kind, item_id: title.id, media_file_id: mediaFileId, client: await api.detectClientProfile() });
  return session;
}

function endSession(id: string | undefined) {
  if (id) void api.endSession(id).catch(() => {});
}

/**
 * prepareDownload opens a playback session and probes the file, so the
 * consent prompt can show the real size and remaining storage. Call
 * discardPrepared when the user declines.
 */
export async function prepareDownload(userId: string, title: VaultTitle, currentCount: number): Promise<PreparedDownload> {
  if (!policy.enabled) throw new VaultError("disabled", "Downloads are turned off by the administrator.");
  if (currentCount >= policy.max_items) {
    throw new VaultError("quota", `Your account can keep up to ${policy.max_items} offline titles on a device. Remove one to add another.`);
  }
  const session = await openSession(title);
  try {
    const info = await probe(sessionPath(session.id, "/download"));
    const estimate = await storageEstimate();
    const admission = checkAdmission({ size: info.size, policy, itemCount: currentCount, estimate });
    if (!admission.ok) throw new VaultError(admission.reason === "too_large" ? "too_large" : "quota", admission.message);
    return {
      ...info,
      userId,
      title,
      sessionId: session.id,
      mediaFileId: session.source_selection?.media_file_id,
      durationMs: session.duration_ms ?? title.durationMs,
      estimate,
      persisted: await storagePersisted(),
    };
  } catch (error) {
    endSession(session.id);
    throw error;
  }
}

export function discardPrepared(prepared: PreparedDownload) {
  endSession(prepared.sessionId);
}

/** startDownload creates the vault record after consent and begins the transfer. */
export async function startDownload(prepared: PreparedDownload): Promise<VaultRecord> {
  const key = vaultKey(prepared.userId, prepared.title.kind, prepared.title.id);
  await userKey(prepared.userId);
  const existing = await getRecord(key);
  const record: VaultRecord = existing ?? {
    key,
    userId: prepared.userId,
    itemKind: prepared.title.kind,
    itemId: prepared.title.id,
    libraryId: prepared.title.libraryId,
    mediaFileId: prepared.mediaFileId,
    title: prepared.title.title,
    mime: prepared.mime,
    size: prepared.size,
    chunkSize: DEFAULT_CHUNK_SIZE,
    etag: prepared.etag,
    durationMs: prepared.durationMs,
    status: "paused",
    hashes: new Array<string | null>(chunkCount(prepared.size, DEFAULT_CHUNK_SIZE)).fill(null),
    bytesStored: 0,
    createdAt: new Date().toISOString(),
  };
  await putRecord(record);
  emit({ type: "update", record });
  schedule(prepared.userId, key, prepared.sessionId);
  return record;
}

export function resumeDownload(userId: string, key: string) {
  schedule(userId, key);
}

function schedule(userId: string, key: string, sessionId?: string) {
  if (active.has(key) || waiting.some((w) => w.key === key)) {
    endSession(sessionId);
    return;
  }
  waiting.push({ userId, key, sessionId });
  pump();
}

function pump() {
  while (active.size < MAX_ACTIVE && waiting.length) {
    const next = waiting.shift()!;
    const entry: Active = { controller: new AbortController(), canceled: false, done: Promise.resolve() };
    active.set(next.key, entry);
    entry.done = withLock(next.key, () => run(next.userId, next.key, entry, next.sessionId))
      .catch(async (error) => {
        const record = await patchRecord(next.key, { status: "paused", error: message(error) }).catch(() => undefined);
        if (record) emit({ type: "update", record });
      })
      .finally(() => {
        active.delete(next.key);
        pump();
      });
  }
  if (waiting.length) emit({ type: "refresh" });
}

async function withLock(key: string, fn: () => Promise<void>): Promise<void> {
  const locks = typeof navigator !== "undefined" ? navigator.locks : undefined;
  if (!locks?.request) return fn();
  await locks.request(`viewdock-vault:${key}`, { ifAvailable: true }, async (lock) => {
    if (!lock) throw new VaultError("busy", "This title is already downloading in another tab.");
    await fn();
  });
}

export async function pauseDownload(key: string) {
  const index = waiting.findIndex((w) => w.key === key);
  if (index >= 0) {
    endSession(waiting[index].sessionId);
    waiting.splice(index, 1);
    emit({ type: "refresh" });
  }
  const entry = active.get(key);
  if (!entry) return;
  entry.controller.abort();
  await entry.done;
}

/** removeDownload cancels any transfer and deletes the title and its chunks. */
export async function removeDownload(key: string) {
  const entry = active.get(key);
  if (entry) {
    entry.canceled = true;
    entry.controller.abort();
    await entry.done;
  }
  const index = waiting.findIndex((w) => w.key === key);
  if (index >= 0) {
    endSession(waiting[index].sessionId);
    waiting.splice(index, 1);
  }
  await deleteRecord(key);
  emit({ type: "remove", key });
}

async function run(userId: string, key: string, entry: Active, initialSession?: string) {
  let sessionId = initialSession;
  let record = await getRecord(key);
  if (!record) {
    endSession(sessionId);
    return;
  }
  const cryptoKey = await userKey(userId);
  record = { ...record, status: "downloading", error: undefined };
  await putRecord(record);
  emit({ type: "update", record });

  const title: VaultTitle = { kind: record.itemKind, id: record.itemId, title: record.title, libraryId: record.libraryId };
  const url = async (fresh: boolean) => {
    if (fresh || !sessionId) {
      endSession(sessionId);
      sessionId = undefined;
      sessionId = (await openSession(title, record?.mediaFileId)).id;
    }
    return sessionPath(sessionId, "/download");
  };

  try {
    for (let round = 0; ; round++) {
      record = await transfer({
        state: record,
        signal: entry.controller.signal,
        url,
        onProgress: (next) => emit({ type: "update", record: next }),
        admit: async (size) => {
          const admission = checkAdmission({ size, policy: { ...policy, max_items: Number.MAX_SAFE_INTEGER }, itemCount: 0, estimate: await storageEstimate() });
          if (!admission.ok) throw new VaultError(admission.reason === "too_large" ? "too_large" : "quota", admission.message);
        },
        sink: {
          commit: async (state, index, plain) => {
            try {
              return await commitChunk(state, index, plain, cryptoKey);
            } catch (error) {
              if (isQuotaError(error)) throw new VaultError("quota", message(error));
              throw error;
            }
          },
          reset: async (state, meta) => {
            await clearChunks(state.key);
            const next: VaultRecord = {
              ...state,
              size: meta.size,
              etag: meta.etag,
              mime: meta.mime || state.mime,
              hashes: new Array<string | null>(chunkCount(meta.size, state.chunkSize)).fill(null),
              bytesStored: 0,
              manifest: undefined,
            };
            await putRecord(next);
            emit({ type: "update", record: next });
            return next;
          },
        },
      });
      record = { ...record, status: "verifying" };
      await putRecord(record);
      emit({ type: "update", record });
      const bad = await verifyStoredChunks(record, cryptoKey, entry.controller.signal);
      if (!bad.length) break;
      const hashes = record.hashes.slice();
      for (const index of bad) hashes[index] = null;
      record = { ...record, hashes, bytesStored: storedBytes(hashes, record.size, record.chunkSize) };
      if (round + 1 >= VERIFY_ROUNDS) {
        record = { ...record, status: "corrupt", error: "Stored data failed verification repeatedly. Remove the title and download it again." };
        await putRecord(record);
        emit({ type: "update", record });
        return;
      }
      await putRecord(record);
    }
    const manifest = await buildManifest({ size: record.size, chunkSize: record.chunkSize, mime: record.mime, etag: record.etag, chunks: record.hashes });
    const downloadedAt = new Date().toISOString();
    record = { ...record, manifest, status: "complete", error: undefined, downloadedAt, expiresAt: expiryFor(downloadedAt, policy.expiry_days) };
    await putRecord(record);
    emit({ type: "update", record });
  } catch (error) {
    if (entry.canceled) return;
    const paused = error instanceof DOMException && error.name === "AbortError";
    const quota = (error instanceof VaultError && (error.code === "quota" || error.code === "too_large")) || isQuotaError(error);
    const latest = (await getRecord(key)) ?? record;
    record = { ...latest, status: paused ? "paused" : quota ? "quota" : "error", error: paused ? undefined : message(error) };
    await putRecord(record);
    emit({ type: "update", record });
  } finally {
    endSession(sessionId);
  }
}

/** verifyStoredChunks decrypts every chunk and returns the indexes that fail. */
export async function verifyStoredChunks(record: VaultRecord, cryptoKey: CryptoKey, signal?: AbortSignal): Promise<number[]> {
  const bad: number[] = [];
  for (let index = 0; index < record.hashes.length; index++) {
    if (signal?.aborted) throw new DOMException("verification paused", "AbortError");
    const stored = await getChunk(record.key, index);
    if (!stored || !record.hashes[index]) {
      bad.push(index);
      continue;
    }
    try {
      const plain = await decryptChunk(cryptoKey, stored, chunkAad(record.key, index));
      if ((await sha256Hex(plain)) !== record.hashes[index]) bad.push(index);
    } catch {
      bad.push(index);
    }
  }
  return bad;
}

/** verifyDownload re-checks a finished title on demand and marks it corrupt on failure. */
export async function verifyDownload(userId: string, key: string): Promise<boolean> {
  const record = await getRecord(key);
  if (!record || record.userId !== userId) return false;
  const check = await verifyManifest(record.manifest);
  const bad = check.ok ? await verifyStoredChunks(record, await userKey(userId)) : [0];
  if (!bad.length) return true;
  const hashes = record.hashes.slice();
  for (const index of bad) hashes[index] = null;
  const next = await patchRecord(key, {
    status: "corrupt",
    hashes: check.ok ? hashes : record.hashes,
    bytesStored: check.ok ? storedBytes(hashes, record.size, record.chunkSize) : record.bytesStored,
    error: check.ok ? `${bad.length} part${bad.length === 1 ? "" : "s"} failed verification. Resume to repair.` : `Integrity manifest is invalid (${check.reason}).`,
  });
  if (next) emit({ type: "update", record: next });
  return false;
}

/**
 * reconcileVault applies expiry and policy, marks transfers interrupted by a
 * closed tab as paused, detects chunks the browser evicted, and migrates
 * titles saved by the previous unencrypted vault.
 */
export async function reconcileVault(userId: string, authoritative: boolean): Promise<ReconcileReport> {
  const report: ReconcileReport = { expired: 0, revoked: 0, evicted: 0, unreadable: 0, migrated: 0 };
  report.migrated = await migrateLegacy(userId).catch(() => 0);
  let records = await listRecords(userId);
  if (records.length && !(await hasUserKey(userId))) {
    for (const record of records) await deleteRecord(record.key);
    report.unreadable = records.length;
    records = [];
  }
  const plan = planPolicy(records, policy, authoritative);
  for (const key of plan.expired) await deleteRecord(key);
  for (const key of plan.revoked) await deleteRecord(key);
  for (const { key, expiresAt } of plan.retime) await patchRecord(key, { expiresAt });
  report.expired = plan.expired.length;
  report.revoked = plan.revoked.length;
  const removed = new Set([...plan.expired, ...plan.revoked]);
  for (const record of records) {
    if (removed.has(record.key) || active.has(record.key)) continue;
    if (record.status === "downloading" || record.status === "verifying") {
      await patchRecord(record.key, { status: "paused" });
    }
    if (record.status !== "complete") continue;
    const stored = await storedChunkIndexes(record.key);
    if (stored.size >= record.hashes.length) continue;
    const hashes = record.hashes.map((hash, index) => (stored.has(index) ? hash : null));
    await patchRecord(record.key, {
      status: "paused",
      hashes,
      manifest: undefined,
      bytesStored: storedBytes(hashes, record.size, record.chunkSize),
      error: "The browser removed part of this title to free space. Resume to download the missing parts.",
    });
    report.evicted++;
  }
  emit({ type: "refresh" });
  return report;
}

async function legacyExists(): Promise<boolean> {
  if (typeof indexedDB === "undefined") return false;
  if (typeof indexedDB.databases !== "function") return true;
  const dbs = await indexedDB.databases();
  return dbs.some((db) => db.name === LEGACY_VAULT_DB);
}

/** migrateLegacy moves unencrypted whole-file items into the encrypted, chunked vault. */
async function migrateLegacy(userId: string): Promise<number> {
  if (!(await legacyExists())) return 0;
  const legacy = await listLegacyVault();
  if (!legacy.length) return 0;
  const cryptoKey = await userKey(userId);
  let migrated = 0;
  for (const item of legacy) {
    const key = vaultKey(userId, item.itemKind, item.itemId);
    if (await getRecord(key)) {
      await removeLegacyItem(item.id);
      continue;
    }
    let record: VaultRecord = {
      key,
      userId,
      itemKind: item.itemKind,
      itemId: item.itemId,
      title: item.title,
      mime: item.mime,
      size: item.blob.size,
      chunkSize: DEFAULT_CHUNK_SIZE,
      durationMs: item.durationMs,
      status: "verifying",
      hashes: new Array<string | null>(chunkCount(item.blob.size, DEFAULT_CHUNK_SIZE)).fill(null),
      bytesStored: 0,
      createdAt: item.downloadedAt,
    };
    try {
      await putRecord(record);
      for (let index = 0; index < record.hashes.length; index++) {
        const start = index * record.chunkSize;
        const plain = new Uint8Array(await item.blob.slice(start, start + record.chunkSize).arrayBuffer());
        record = await commitChunk(record, index, plain, cryptoKey);
      }
      const manifest = await buildManifest({ size: record.size, chunkSize: record.chunkSize, mime: record.mime, chunks: record.hashes });
      await putRecord({ ...record, manifest, status: "complete", downloadedAt: item.downloadedAt, expiresAt: expiryFor(item.downloadedAt, policy.expiry_days) });
      await removeLegacyItem(item.id);
      migrated++;
    } catch (error) {
      await deleteRecord(key).catch(() => {});
      if (isQuotaError(error)) break;
      throw error;
    }
  }
  return migrated;
}

/**
 * signOutVault stops every transfer and deletes the vault database, keys
 * included, plus the cached policy. The service worker does the same on
 * "viewdock:clear-user-caches"; this covers pages it does not control.
 */
export async function signOutVault(userId?: string): Promise<void> {
  for (const pending of waiting.splice(0)) endSession(pending.sessionId);
  const running = [...active.values()];
  for (const entry of running) {
    entry.canceled = true;
    entry.controller.abort();
  }
  await Promise.allSettled(running.map((entry) => entry.done));
  if (userId) forgetCachedPolicy(userId);
  policy = DEFAULT_OFFLINE_POLICY;
  await clearVault();
  // The previous vault kept unencrypted files; they must not outlive the session either.
  if (typeof indexedDB !== "undefined") indexedDB.deleteDatabase(LEGACY_VAULT_DB);
  emit({ type: "refresh" });
}

export async function listDownloads(userId: string): Promise<VaultRecord[]> {
  return listRecords(userId);
}

/**
 * serviceWorkerServesVault confirms the active service worker understands
 * vault playback; an older worker would pass the request to the network.
 */
export async function serviceWorkerServesVault(timeoutMs = 1500): Promise<boolean> {
  const controller = typeof navigator !== "undefined" ? navigator.serviceWorker?.controller : null;
  if (!controller) return false;
  return new Promise((resolve) => {
    const channel = new MessageChannel();
    const timer = setTimeout(() => resolve(false), timeoutMs);
    channel.port1.onmessage = (event) => {
      clearTimeout(timer);
      resolve(Boolean((event.data as { vault?: number } | null)?.vault));
    };
    controller.postMessage({ type: "viewdock:vault-ping" }, [channel.port2]);
  });
}

/**
 * vaultBlob decrypts a title into a Blob for browsers without a controlling
 * service worker. It holds the whole title in memory, so callers cap its size.
 */
export async function vaultBlob(userId: string, key: string): Promise<Blob> {
  const record = await getRecord(key);
  if (!record || record.userId !== userId || record.status !== "complete" || !record.manifest) throw new Error("This title is not ready for offline playback.");
  const cryptoKey = await userKey(userId);
  const parts: ArrayBuffer[] = [];
  for (let index = 0; index < record.manifest.chunkCount; index++) {
    const stored = await getChunk(key, index);
    if (!stored) throw new Error("Part of this title is missing from device storage.");
    const plain = await decryptChunk(cryptoKey, stored, chunkAad(key, index));
    if ((await sha256Hex(plain)) !== record.manifest.chunks[index]) throw new Error("This title failed its integrity check.");
    parts.push(plain);
  }
  return new Blob(parts, { type: record.manifest.mime });
}
