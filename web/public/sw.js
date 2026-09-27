const SHELL_CACHE = "viewdock-shell-v2";
const ASSET_CACHE = "viewdock-assets-v1";
const ART_CACHE = "viewdock-artwork-v1";
const KNOWN = [SHELL_CACHE, ASSET_CACHE, ART_CACHE];
const SHELL = ["/", "/site.webmanifest"];
const MAX_ASSETS = 200;
const MAX_ART = 400;

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(SHELL_CACHE).then((cache) => cache.addAll(SHELL)).then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) => Promise.all(
      keys.filter((key) => key.startsWith("viewdock-") && !KNOWN.includes(key) && !key.startsWith("viewdock-vault")).map((key) => caches.delete(key)),
    )).then(() => self.clients.claim()),
  );
});

// Signed-out devices must not keep per-user artwork or Offline Vault data.
// Deleting the vault database also deletes the per-user keys, so any chunk
// the browser has not yet reclaimed is unreadable.
self.addEventListener("message", (event) => {
  const type = event.data && event.data.type;
  if (type === "viewdock:clear-user-caches") {
    event.waitUntil(Promise.all([caches.delete(ART_CACHE), clearVault()]));
  } else if (type === "viewdock:vault-ping" && event.ports && event.ports[0]) {
    event.ports[0].postMessage({ vault: VAULT_PROTOCOL });
  }
});

// Offline Vault playback. The schema must match web/src/lib/vault/db.ts.
const VAULT_DB = "viewdock-vault-v2";
const VAULT_DB_VERSION = 1;
const LEGACY_VAULT_DB = "viewdock-vault";
const VAULT_PREFIX = "/vault-media/";
const VAULT_PROTOCOL = 1;
const IV_BYTES = 12;
let vaultDB = null;

function upgradeVault(db) {
  if (!db.objectStoreNames.contains("keys")) db.createObjectStore("keys", { keyPath: "userId" });
  if (!db.objectStoreNames.contains("items")) db.createObjectStore("items", { keyPath: "key" }).createIndex("userId", "userId");
  if (!db.objectStoreNames.contains("chunks")) db.createObjectStore("chunks");
}

function openVault() {
  if (vaultDB) return vaultDB;
  vaultDB = new Promise((resolve, reject) => {
    const request = indexedDB.open(VAULT_DB, VAULT_DB_VERSION);
    request.onupgradeneeded = () => upgradeVault(request.result);
    request.onsuccess = () => {
      const db = request.result;
      db.onversionchange = () => {
        db.close();
        vaultDB = null;
      };
      db.onclose = () => {
        vaultDB = null;
      };
      resolve(db);
    };
    request.onerror = () => reject(request.error);
  }).catch((error) => {
    vaultDB = null;
    throw error;
  });
  return vaultDB;
}

function vaultGet(db, store, key) {
  return new Promise((resolve, reject) => {
    const request = db.transaction(store).objectStore(store).get(key);
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

function vaultMark(db, key, patch) {
  return new Promise((resolve) => {
    const tx = db.transaction("items", "readwrite");
    const store = tx.objectStore("items");
    const request = store.get(key);
    request.onsuccess = () => {
      if (request.result) store.put(Object.assign({}, request.result, patch));
    };
    tx.oncomplete = () => resolve();
    tx.onerror = () => resolve();
    tx.onabort = () => resolve();
  });
}

function deleteDatabase(name) {
  return new Promise((resolve) => {
    const request = indexedDB.deleteDatabase(name);
    request.onsuccess = () => resolve();
    request.onerror = () => resolve();
    // Pages release their connections on versionchange; deletion completes then.
    request.onblocked = () => {};
  });
}

async function clearVault() {
  const open = vaultDB;
  vaultDB = null;
  if (open) {
    try {
      (await open).close();
    } catch (error) {
      // The connection never opened; nothing to close.
    }
  }
  await Promise.all([deleteDatabase(VAULT_DB), deleteDatabase(LEGACY_VAULT_DB)]);
}

// vault-range:start
function parseRange(header, size) {
  if (!header) return { kind: "full" };
  const match = /^\s*bytes\s*=\s*(\d*)\s*-\s*(\d*)\s*$/i.exec(header);
  if (!match) return { kind: "full" };
  const a = match[1];
  const b = match[2];
  if (a === "" && b === "") return { kind: "full" };
  if (size <= 0) return { kind: "unsatisfiable" };
  if (a === "") {
    const suffix = Number(b);
    if (!Number.isSafeInteger(suffix)) return { kind: "full" };
    if (suffix === 0) return { kind: "unsatisfiable" };
    return { kind: "partial", start: Math.max(0, size - suffix), end: size - 1 };
  }
  const start = Number(a);
  if (!Number.isSafeInteger(start)) return { kind: "full" };
  if (start >= size) return { kind: "unsatisfiable" };
  if (b === "") return { kind: "partial", start, end: size - 1 };
  const end = Number(b);
  if (!Number.isSafeInteger(end) || end < start) return { kind: "full" };
  return { kind: "partial", start, end: Math.min(end, size - 1) };
}

function chunkSpan(start, end, chunkSize) {
  return { first: Math.floor(start / chunkSize), last: Math.floor(end / chunkSize) };
}
// vault-range:end

function hex(buffer) {
  const bytes = new Uint8Array(buffer);
  let out = "";
  for (let i = 0; i < bytes.length; i++) out += bytes[i].toString(16).padStart(2, "0");
  return out;
}

function vaultError(status, text, headers) {
  return new Response(text, { status, headers: Object.assign({ "Content-Type": "text/plain", "Cache-Control": "no-store" }, headers || {}) });
}

async function vaultResponse(request, key) {
  let db;
  try {
    db = await openVault();
  } catch (error) {
    return vaultError(503, "Offline storage is unavailable.");
  }
  const record = await vaultGet(db, "items", key);
  const manifest = record && record.manifest;
  if (!record || record.status !== "complete" || !manifest) return vaultError(404, "This title is not in the Offline Vault.");
  if (record.expiresAt && Date.parse(record.expiresAt) <= Date.now()) return vaultError(410, "This offline title has expired.");
  const keyRow = await vaultGet(db, "keys", record.userId);
  if (!keyRow || !keyRow.key) return vaultError(404, "This offline title can no longer be decrypted.");

  const size = manifest.size;
  const chunkSize = manifest.chunkSize;
  const range = parseRange(request.headers.get("Range"), size);
  if (range.kind === "unsatisfiable") return vaultError(416, "Range not satisfiable.", { "Content-Range": `bytes */${size}` });
  const start = range.kind === "partial" ? range.start : 0;
  const end = range.kind === "partial" ? range.end : size - 1;
  const span = chunkSpan(start, end, chunkSize);
  const encoder = new TextEncoder();
  let index = span.first;

  const body = new ReadableStream({
    async pull(controller) {
      if (index > span.last) {
        controller.close();
        return;
      }
      const current = index++;
      try {
        const stored = await vaultGet(db, "chunks", [key, current]);
        if (!stored) throw new Error("missing chunk");
        const iv = new Uint8Array(stored, 0, IV_BYTES);
        const sealed = new Uint8Array(stored, IV_BYTES);
        const aad = encoder.encode(`viewdock-vault:${key}#${current}`);
        const plain = await crypto.subtle.decrypt({ name: "AES-GCM", iv, additionalData: aad }, keyRow.key, sealed);
        if (hex(await crypto.subtle.digest("SHA-256", plain)) !== manifest.chunks[current]) throw new Error("digest mismatch");
        const chunkStart = current * chunkSize;
        const from = Math.max(start, chunkStart) - chunkStart;
        const to = Math.min(end, chunkStart + plain.byteLength - 1) - chunkStart + 1;
        controller.enqueue(new Uint8Array(plain, from, to - from));
      } catch (error) {
        await vaultMark(db, key, { status: "corrupt", error: "Stored data failed its integrity check during playback. Resume to repair or remove the title." });
        controller.error(error);
      }
    },
  }, { highWaterMark: 1 });

  const headers = {
    "Content-Type": manifest.mime || "video/mp4",
    "Content-Length": String(end - start + 1),
    "Accept-Ranges": "bytes",
    "Cache-Control": "no-store",
  };
  if (range.kind === "partial") {
    headers["Content-Range"] = `bytes ${start}-${end}/${size}`;
    return new Response(body, { status: 206, headers });
  }
  return new Response(body, { status: 200, headers });
}

async function trim(cacheName, max) {
  const cache = await caches.open(cacheName);
  const keys = await cache.keys();
  for (let i = 0; i < keys.length - max; i++) {
    await cache.delete(keys[i]);
  }
}

function cacheable(response) {
  return response && response.ok && response.type === "basic";
}

self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET") return;

  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;

  if (url.pathname.startsWith(VAULT_PREFIX)) {
    let key;
    try {
      key = decodeURIComponent(url.pathname.slice(VAULT_PREFIX.length));
    } catch (error) {
      event.respondWith(vaultError(400, "Malformed offline title."));
      return;
    }
    event.respondWith(vaultResponse(request, key).catch(() => vaultError(500, "Offline playback failed.")));
    return;
  }

  if (request.mode === "navigate") {
    event.respondWith(
      fetch(request).then((response) => {
        const type = response.headers.get("Content-Type") || "";
        if (cacheable(response) && type.includes("text/html")) {
          const copy = response.clone();
          caches.open(SHELL_CACHE).then((cache) => cache.put("/", copy));
        }
        return response;
      }).catch(() => caches.match("/", { cacheName: SHELL_CACHE }).then((response) => response || Response.error())),
    );
    return;
  }

  if (url.pathname.startsWith("/assets/")) {
    event.respondWith(
      caches.match(request, { cacheName: ASSET_CACHE }).then((cached) => cached || fetch(request).then((response) => {
        if (cacheable(response)) {
          const copy = response.clone();
          caches.open(ASSET_CACHE).then((cache) => cache.put(request, copy)).then(() => trim(ASSET_CACHE, MAX_ASSETS));
        }
        return response;
      })),
    );
    return;
  }

  if (url.pathname.startsWith("/api/v1/artwork/")) {
    event.respondWith(
      caches.open(ART_CACHE).then(async (cache) => {
        const cached = await cache.match(request);
        const network = fetch(request).then((response) => {
          if (cacheable(response)) {
            cache.put(request, response.clone()).then(() => trim(ART_CACHE, MAX_ART));
          }
          return response;
        });
        if (cached) {
          network.catch(() => {});
          return cached;
        }
        return network;
      }),
    );
  }
});
