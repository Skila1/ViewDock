export type QueuedMutation = {
  id: string;
  path: string;
  method: string;
  body?: unknown;
  headers?: Record<string, string>;
  createdAt: number;
  idempotencyKey: string;
  attempts?: number;
  status?: "pending" | "conflict";
  conflict?: unknown;
  // Pending mutations sharing a key are superseded by the newest one.
  coalesceKey?: string;
};

// sent: applied; conflict: server state differs and needs a user decision;
// dropped: target no longer exists (404/410) so replay can never succeed;
// failed: transient, retry on the next reconnect.
export type MutationResult = "sent" | "conflict" | "dropped" | "failed";

export type ReplaySummary = { sent: number; conflicts: number; dropped: number; failed: number };

const DB_NAME = "viewdock-offline";
const STORE_NAME = "mutations";
const DB_VERSION = 1;
const MAX_ATTEMPTS = 20;
const QUEUE_EVENT = "viewdock:offline-queue";

function makeId(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(DB_NAME, DB_VERSION);
    request.onerror = () => reject(request.error ?? new Error("unable to open offline storage"));
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(STORE_NAME)) {
        db.createObjectStore(STORE_NAME, { keyPath: "id" });
      }
    };
    request.onsuccess = () => resolve(request.result);
  });
}

function transaction<T>(mode: IDBTransactionMode, action: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return openDB().then((db) => new Promise((resolve, reject) => {
    const request = action(db.transaction(STORE_NAME, mode).objectStore(STORE_NAME));
    request.onerror = () => reject(request.error ?? new Error("offline storage request failed"));
    request.onsuccess = () => resolve(request.result);
    request.transaction?.addEventListener("complete", () => db.close());
  }));
}

function notify() {
  if (typeof window !== "undefined") window.dispatchEvent(new Event(QUEUE_EVENT));
}

export function onQueueChange(listener: () => void): () => void {
  window.addEventListener(QUEUE_EVENT, listener);
  return () => window.removeEventListener(QUEUE_EVENT, listener);
}

// Replay order follows createdAt, so it must be unique even within one millisecond.
let lastCreatedAt = 0;

export async function enqueueMutation(input: Omit<QueuedMutation, "id" | "createdAt" | "idempotencyKey">): Promise<QueuedMutation> {
  lastCreatedAt = Math.max(Date.now(), lastCreatedAt + 1);
  const mutation: QueuedMutation = {
    ...input,
    id: makeId(),
    createdAt: lastCreatedAt,
    idempotencyKey: makeId(),
    attempts: 0,
    status: "pending",
  };
  if (mutation.coalesceKey) {
    for (const existing of await listQueuedMutations()) {
      if (existing.coalesceKey === mutation.coalesceKey && existing.status !== "conflict") {
        await transaction("readwrite", (store) => store.delete(existing.id));
      }
    }
  }
  await transaction("readwrite", (store) => store.add(mutation));
  notify();
  return mutation;
}

// resolveConflict either discards the local change or re-queues it with
// force set so the server accepts it over its newer state.
export async function resolveConflict(id: string, choice: "keep" | "discard"): Promise<void> {
  const mutation = (await listQueuedMutations()).find((m) => m.id === id);
  if (!mutation) return;
  if (choice === "discard") {
    await removeQueuedMutation(id);
    return;
  }
  const body = mutation.body && typeof mutation.body === "object" ? { ...(mutation.body as Record<string, unknown>), force: true } : mutation.body;
  await updateQueuedMutation({ ...mutation, body, status: "pending", conflict: undefined, attempts: 0 });
  notify();
}

export async function listQueuedMutations(): Promise<QueuedMutation[]> {
  const rows = await transaction<QueuedMutation[]>("readonly", (store) => store.getAll());
  return rows.sort((a, b) => a.createdAt - b.createdAt);
}

export async function removeQueuedMutation(id: string): Promise<void> {
  await transaction("readwrite", (store) => store.delete(id));
  notify();
}

async function updateQueuedMutation(mutation: QueuedMutation): Promise<void> {
  await transaction("readwrite", (store) => store.put(mutation));
}

let replaying: Promise<ReplaySummary> | null = null;

// replayQueuedMutations sends pending mutations in order. Conflicts are kept
// and exposed instead of being overwritten; concurrent replays share one run.
export function replayQueuedMutations(send: (mutation: QueuedMutation) => Promise<{ result: MutationResult; detail?: unknown }>): Promise<ReplaySummary> {
  if (replaying) return replaying;
  replaying = (async () => {
    const summary: ReplaySummary = { sent: 0, conflicts: 0, dropped: 0, failed: 0 };
    for (const mutation of await listQueuedMutations()) {
      if (mutation.status === "conflict") {
        summary.conflicts++;
        continue;
      }
      const { result, detail } = await send(mutation);
      if (result === "sent" || result === "dropped") {
        await removeQueuedMutation(mutation.id);
        summary[result === "sent" ? "sent" : "dropped"]++;
      } else if (result === "conflict") {
        await updateQueuedMutation({ ...mutation, status: "conflict", conflict: detail });
        summary.conflicts++;
      } else {
        const attempts = (mutation.attempts ?? 0) + 1;
        if (attempts >= MAX_ATTEMPTS) {
          await removeQueuedMutation(mutation.id);
          summary.dropped++;
        } else {
          await updateQueuedMutation({ ...mutation, attempts });
          summary.failed++;
          break;
        }
      }
    }
    notify();
    return summary;
  })().finally(() => {
    replaying = null;
  });
  return replaying;
}
