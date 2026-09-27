import "fake-indexeddb/auto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { beforeEach, describe, expect, it } from "vitest";
import {
  clearUserVault,
  commitChunk,
  getChunk,
  getRecord,
  listRecords,
  openVaultDB,
  putRecord,
  storedChunkIndexes,
  userKey,
  vaultKey,
  VAULT_DB,
  type VaultRecord,
} from "./db";
import { chunkAad, decryptChunk } from "./crypto";
import { buildManifest } from "./manifest";
import { chunkCount } from "./range";

const CHUNK = 1024;
const ORIGIN = "http://localhost";

type Listener = (event: unknown) => void;

function loadServiceWorker() {
  const listeners: Record<string, Listener> = {};
  const fakeSelf = {
    location: { origin: ORIGIN },
    addEventListener: (type: string, fn: Listener) => {
      listeners[type] = fn;
    },
    skipWaiting: () => Promise.resolve(),
    clients: { claim: () => Promise.resolve() },
  };
  const fakeCaches = { open: async () => ({}), keys: async () => [], match: async () => undefined, delete: async () => true };
  const source = readFileSync(resolve(process.cwd(), "public", "sw.js"), "utf8");
  new Function("self", "caches", source)(fakeSelf, fakeCaches);

  const fetchVia = async (path: string, headers: Record<string, string> = {}) => {
    let response: Promise<Response> | undefined;
    listeners.fetch({ request: new Request(`${ORIGIN}${path}`, { headers }), respondWith: (p: Promise<Response>) => (response = p) });
    if (!response) throw new Error("service worker did not respond");
    return response;
  };
  const message = async (data: unknown, ports: MessagePort[] = []) => {
    let pending: Promise<unknown> = Promise.resolve();
    listeners.message({ data, ports, waitUntil: (p: Promise<unknown>) => (pending = p) });
    await pending;
  };
  return { fetchVia, message };
}

function makeData(size: number): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(size);
  for (let i = 0; i < size; i++) out[i] = (i * 7 + 3) & 0xff;
  return out;
}

async function storeTitle(userId: string, itemId: string, data: Uint8Array<ArrayBuffer>, patch: Partial<VaultRecord> = {}): Promise<VaultRecord> {
  const key = vaultKey(userId, "movie", itemId);
  const cryptoKey = await userKey(userId);
  let record: VaultRecord = {
    key,
    userId,
    itemKind: "movie",
    itemId,
    title: `Title ${itemId}`,
    mime: "video/mp4",
    size: data.length,
    chunkSize: CHUNK,
    status: "downloading",
    hashes: new Array<string | null>(chunkCount(data.length, CHUNK)).fill(null),
    bytesStored: 0,
    createdAt: new Date().toISOString(),
  };
  await putRecord(record);
  for (let index = 0; index < record.hashes.length; index++) {
    record = await commitChunk(record, index, data.slice(index * CHUNK, (index + 1) * CHUNK), cryptoKey);
  }
  const manifest = await buildManifest({ size: record.size, chunkSize: CHUNK, mime: record.mime, chunks: record.hashes });
  record = { ...record, manifest, status: "complete", downloadedAt: new Date().toISOString(), ...patch };
  await putRecord(record);
  return record;
}

async function body(res: Response): Promise<Uint8Array> {
  return new Uint8Array(await res.arrayBuffer());
}

async function wipe() {
  const db = await openVaultDB();
  const tx = db.transaction(["keys", "items", "chunks"], "readwrite");
  for (const name of ["keys", "items", "chunks"]) tx.objectStore(name).clear();
  await new Promise((resolve) => (tx.oncomplete = resolve));
}

describe("vault storage", () => {
  beforeEach(wipe);

  it("stores chunks encrypted and keeps one non-extractable key per user", async () => {
    const data = makeData(3000);
    const record = await storeTitle("alice", "m1", data);
    expect(record.bytesStored).toBe(3000);
    expect(await storedChunkIndexes(record.key)).toEqual(new Set([0, 1, 2]));
    const sealed = await getChunk(record.key, 1);
    expect(sealed!.byteLength).toBe(CHUNK + 28);
    expect(new Uint8Array(sealed!).subarray(12, 40)).not.toEqual(data.subarray(CHUNK, CHUNK + 28));
    const key = await userKey("alice");
    expect(key.extractable).toBe(false);
    expect(await userKey("alice")).toBeTruthy();
    const plain = new Uint8Array(await decryptChunk(key, sealed!, chunkAad(record.key, 1)));
    expect(plain).toEqual(data.subarray(CHUNK, 2 * CHUNK));
    await expect(decryptChunk(await userKey("bob"), sealed!, chunkAad(record.key, 1))).rejects.toThrow();
  });

  it("clears one user's vault without touching another's", async () => {
    const a = await storeTitle("alice", "m1", makeData(1500));
    const b = await storeTitle("bob", "m1", makeData(1500));
    await clearUserVault("alice");
    expect(await listRecords("alice")).toEqual([]);
    expect(await storedChunkIndexes(a.key)).toEqual(new Set());
    expect((await listRecords("bob")).map((r) => r.key)).toEqual([b.key]);
  });
});

describe("service worker vault playback", () => {
  beforeEach(wipe);

  it("serves decrypted byte ranges across chunk boundaries", async () => {
    const data = makeData(5000);
    const record = await storeTitle("alice", "m1", data);
    const sw = loadServiceWorker();
    const url = `/vault-media/${encodeURIComponent(record.key)}`;

    const partial = await sw.fetchVia(url, { Range: "bytes=1000-2500" });
    expect(partial.status).toBe(206);
    expect(partial.headers.get("Content-Range")).toBe("bytes 1000-2500/5000");
    expect(partial.headers.get("Content-Length")).toBe("1501");
    expect(partial.headers.get("Accept-Ranges")).toBe("bytes");
    expect(partial.headers.get("Content-Type")).toBe("video/mp4");
    expect(await body(partial)).toEqual(data.subarray(1000, 2501));

    const tail = await sw.fetchVia(url, { Range: "bytes=4990-" });
    expect(await body(tail)).toEqual(data.subarray(4990));

    const full = await sw.fetchVia(url);
    expect(full.status).toBe(200);
    expect(await body(full)).toEqual(data);

    const bad = await sw.fetchVia(url, { Range: "bytes=6000-" });
    expect(bad.status).toBe(416);
    expect(bad.headers.get("Content-Range")).toBe("bytes */5000");
  });

  it("refuses unknown, unfinished and expired titles", async () => {
    const sw = loadServiceWorker();
    expect((await sw.fetchVia(`/vault-media/${encodeURIComponent("alice|movie|missing")}`)).status).toBe(404);
    const partial = await storeTitle("alice", "p1", makeData(1500), { status: "paused" });
    expect((await sw.fetchVia(`/vault-media/${encodeURIComponent(partial.key)}`)).status).toBe(404);
    const expired = await storeTitle("alice", "e1", makeData(1500), { expiresAt: new Date(Date.now() - 1000).toISOString() });
    expect((await sw.fetchVia(`/vault-media/${encodeURIComponent(expired.key)}`)).status).toBe(410);
  });

  it("fails the stream and marks the title corrupt when a chunk was altered", async () => {
    const record = await storeTitle("alice", "m1", makeData(3000));
    const db = await openVaultDB();
    const sealed = new Uint8Array((await getChunk(record.key, 1))!.slice(0));
    sealed[20] ^= 0xff;
    const tx = db.transaction("chunks", "readwrite");
    tx.objectStore("chunks").put(sealed.buffer, [record.key, 1]);
    await new Promise((resolve) => (tx.oncomplete = resolve));

    const sw = loadServiceWorker();
    const res = await sw.fetchVia(`/vault-media/${encodeURIComponent(record.key)}`);
    await expect(res.arrayBuffer()).rejects.toThrow();
    expect((await getRecord(record.key))?.status).toBe("corrupt");
  });

  it("answers the vault protocol ping", async () => {
    const sw = loadServiceWorker();
    const channel = new MessageChannel();
    const reply = new Promise((resolve) => (channel.port1.onmessage = (event) => resolve(event.data)));
    await sw.message({ type: "viewdock:vault-ping" }, [channel.port2]);
    expect(await reply).toEqual({ vault: 1 });
    channel.port1.close();
  });

  it("deletes the vault, including keys, on sign-out", async () => {
    await storeTitle("alice", "m1", makeData(1500));
    const sw = loadServiceWorker();
    await sw.message({ type: "viewdock:clear-user-caches" });
    const names = (await indexedDB.databases()).map((d) => d.name);
    expect(names).not.toContain(VAULT_DB);
    expect(await listRecords("alice")).toEqual([]);
  });
});
