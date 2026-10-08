// Last-known-good API snapshots for degraded operation. Only an allowlist of
// catalogue, profile and settings reads is stored; admin, auth and playback
// responses never are. Entries are scoped per user, replaced on every
// successful read (so permission revocations propagate) and cleared on logout.

const DB_NAME = "viewdock-snapshots";
const STORE = "responses";
const DB_VERSION = 1;
const MAX_ENTRIES = 400;
const SCOPE_KEY = "viewdock.snapshot.scope";

const CACHEABLE: RegExp[] = [
  /^\/api\/v1\/system$/,
  /^\/api\/v1\/me$/,
  /^\/api\/v1\/me\/preferences$/,
  /^\/api\/v1\/movies(\/[^/]+)?$/,
  /^\/api\/v1\/series(\/[^/]+)?$/,
  /^\/api\/v1\/series\/[^/]+\/next$/,
  /^\/api\/v1\/episodes\/[^/]+$/,
  /^\/api\/v1\/collections(\/[^/]+)?$/,
  /^\/api\/v1\/libraries$/,
  /^\/api\/v1\/playback\/continue$/,
  /^\/api\/v1\/household$/,
];

type Entry = { key: string; scope: string; path: string; body: unknown; savedAt: number };

export function isCacheable(path: string): boolean {
  const clean = path.split("?")[0];
  return CACHEABLE.some((re) => re.test(clean));
}

export function snapshotScope(): string {
  try {
    return localStorage.getItem(SCOPE_KEY) ?? "anon";
  } catch {
    return "anon";
  }
}

export function setSnapshotScope(userId: string | null) {
  try {
    if (userId) localStorage.setItem(SCOPE_KEY, userId);
    else localStorage.removeItem(SCOPE_KEY);
  } catch {
    /* storage unavailable */
  }
}

let dbPromise: Promise<IDBDatabase> | null = null;

function openDB(): Promise<IDBDatabase> {
  if (typeof indexedDB === "undefined") return Promise.reject(new Error("indexedDB unavailable"));
  if (!dbPromise) {
    dbPromise = new Promise((resolve, reject) => {
      const req = indexedDB.open(DB_NAME, DB_VERSION);
      req.onupgradeneeded = () => {
        const store = req.result.createObjectStore(STORE, { keyPath: "key" });
        store.createIndex("scope", "scope");
        store.createIndex("savedAt", "savedAt");
      };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => {
        dbPromise = null;
        reject(req.error ?? new Error("snapshot cache unavailable"));
      };
    });
  }
  return dbPromise;
}

export async function saveSnapshot(path: string, body: unknown): Promise<void> {
  if (!isCacheable(path)) return;
  const scope = snapshotScope();
  const db = await openDB();
  const entry: Entry = { key: `${scope}|${path}`, scope, path, body, savedAt: Date.now() };
  await new Promise<void>((resolve, reject) => {
    const tx = db.transaction(STORE, "readwrite");
    tx.objectStore(STORE).put(entry);
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
  });
  void prune(db);
}

export async function loadSnapshot<T>(path: string): Promise<{ body: T; savedAt: number } | null> {
  if (!isCacheable(path)) return null;
  const db = await openDB();
  return new Promise((resolve) => {
    const req = db.transaction(STORE, "readonly").objectStore(STORE).get(`${snapshotScope()}|${path}`);
    req.onsuccess = () => {
      const entry = req.result as Entry | undefined;
      resolve(entry ? { body: entry.body as T, savedAt: entry.savedAt } : null);
    };
    req.onerror = () => resolve(null);
  });
}

export async function clearSnapshots(): Promise<void> {
  const db = await openDB().catch(() => null);
  if (!db) return;
  await new Promise<void>((resolve) => {
    const tx = db.transaction(STORE, "readwrite");
    tx.objectStore(STORE).clear();
    tx.oncomplete = () => resolve();
    tx.onerror = () => resolve();
  });
}

async function prune(db: IDBDatabase) {
  const count = await new Promise<number>((resolve) => {
    const req = db.transaction(STORE, "readonly").objectStore(STORE).count();
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => resolve(0);
  });
  if (count <= MAX_ENTRIES) return;
  let excess = count - MAX_ENTRIES;
  const tx = db.transaction(STORE, "readwrite");
  const cursorReq = tx.objectStore(STORE).index("savedAt").openCursor();
  cursorReq.onsuccess = () => {
    const cursor = cursorReq.result;
    if (!cursor || excess <= 0) return;
    cursor.delete();
    excess--;
    cursor.continue();
  };
}
