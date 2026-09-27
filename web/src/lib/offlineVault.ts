const DB_NAME = "viewdock-vault";
const DB_VERSION = 1;
const STORE = "media";

export type VaultItem = {
  id: string;
  itemKind: "movie" | "episode";
  itemId: string;
  title: string;
  mime: string;
  size: number;
  durationMs?: number;
  downloadedAt: string;
  blob: Blob;
};

function openVault(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(STORE)) request.result.createObjectStore(STORE, { keyPath: "id" });
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error("offline vault unavailable"));
  });
}

export async function listVault(): Promise<VaultItem[]> {
  const db = await openVault();
  return new Promise((resolve, reject) => {
    const request = db.transaction(STORE, "readonly").objectStore(STORE).getAll();
    request.onsuccess = () => resolve((request.result as VaultItem[]).sort((a, b) => b.downloadedAt.localeCompare(a.downloadedAt)));
    request.onerror = () => reject(request.error ?? new Error("offline vault read failed"));
  });
}

export async function saveVaultItem(item: VaultItem): Promise<void> {
  const db = await openVault();
  await new Promise<void>((resolve, reject) => {
    const request = db.transaction(STORE, "readwrite").objectStore(STORE).put(item);
    request.onsuccess = () => resolve();
    request.onerror = () => reject(request.error ?? new Error("offline storage quota exceeded"));
  });
}

export async function removeVaultItem(id: string): Promise<void> {
  const db = await openVault();
  await new Promise<void>((resolve, reject) => {
    const request = db.transaction(STORE, "readwrite").objectStore(STORE).delete(id);
    request.onsuccess = () => resolve();
    request.onerror = () => reject(request.error ?? new Error("offline vault delete failed"));
  });
}

export async function storageEstimate(): Promise<StorageEstimate> {
  if (navigator.storage?.estimate) return navigator.storage.estimate();
  return {};
}

export async function persistStorage(): Promise<boolean> {
  if (navigator.storage?.persist) return navigator.storage.persist();
  return false;
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const power = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / 1024 ** power).toFixed(power ? 1 : 0)} ${units[power]}`;
}