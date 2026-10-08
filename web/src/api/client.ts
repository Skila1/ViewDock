import type { ErrorBody } from "@/types/api.gen";
import { enqueueMutation, replayQueuedMutations, type ReplaySummary } from "@/lib/offlineQueue";
import { isCacheable, loadSnapshot, saveSnapshot } from "@/lib/snapshotCache";
import { embedHeaders } from "@/lib/discordActivity";

export class ApiError extends Error {
  status: number;
  code?: string;
  body?: unknown;

  constructor(status: number, message: string, code?: string, body?: unknown) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.body = body;
  }
}

const CSRF_COOKIE = "vd_csrf";
const CSRF_HEADER = "X-CSRF-Token";

let csrfToken: string | null = null;
let csrfInflight: Promise<string | null> | null = null;

function readCsrfCookie(): string | null {
  if (typeof document === "undefined") return null;
  const parts = document.cookie.split(";");
  for (const part of parts) {
    const [k, ...rest] = part.trim().split("=");
    if (k === CSRF_COOKIE) return decodeURIComponent(rest.join("="));
  }
  return null;
}

export function clearCsrf(): void {
  csrfToken = null;
}

export async function ensureCsrf(): Promise<string | null> {
  const cookie = readCsrfCookie();
  if (cookie) {
    csrfToken = cookie;
    return cookie;
  }
  if (csrfToken) return csrfToken;
  if (!csrfInflight) {
    csrfInflight = fetch("/api/v1/auth/csrf", { credentials: "include", headers: embedHeaders() })
      .then(async (res) => {
        if (!res.ok) return readCsrfCookie();
        const json = (await res.json().catch(() => ({}))) as { token?: string };
        csrfToken = json.token ?? readCsrfCookie();
        return csrfToken;
      })
      .finally(() => {
        csrfInflight = null;
      });
  }
  return csrfInflight;
}

export type RequestOpts = {
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  signal?: AbortSignal;
  raw?: boolean;
  queueWhenOffline?: boolean;
  // When the request cannot reach a server, queue this mutation instead of the
  // original one (for example a session-independent progress update).
  offlineFallback?: { path: string; method: string; body?: unknown };
};

const UNAVAILABLE = new Set([502, 503, 504]);

async function queueOffline(path: string, opts: RequestOpts, method: string, headers: Record<string, string>, cause: unknown): Promise<never> {
  const fallback = opts.offlineFallback;
  if (fallback) {
    await enqueueMutation({ path: fallback.path, method: fallback.method, body: fallback.body, coalesceKey: `${fallback.method} ${fallback.path}` });
  } else {
    await enqueueMutation({ path, method, body: opts.body, headers: stripVolatile(headers) });
  }
  throw new ApiError(0, "request queued until connectivity returns", "offline_queued", cause);
}

function stripVolatile(headers: Record<string, string>): Record<string, string> {
  const out = { ...headers };
  delete out[CSRF_HEADER];
  return out;
}

async function parseBody(res: Response): Promise<unknown> {
  if (res.status === 204) return null;
  const text = await res.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

export async function request<T>(path: string, opts: RequestOpts = {}): Promise<T> {
  const method = (opts.method ?? "GET").toUpperCase();
  const headers: Record<string, string> = { Accept: "application/json", ...embedHeaders(), ...opts.headers };
  const mutating = !["GET", "HEAD", "OPTIONS"].includes(method);

  if (mutating) {
    const token = (await ensureCsrf()) ?? readCsrfCookie();
    if (token) headers[CSRF_HEADER] = token;
  }

  let body: BodyInit | undefined;
  if (opts.body instanceof Blob || opts.body instanceof ArrayBuffer || opts.body instanceof FormData) {
    body = opts.body as BodyInit;
  } else if (opts.body !== undefined) {
    headers["Content-Type"] = headers["Content-Type"] ?? "application/json";
    body = JSON.stringify(opts.body);
  }

  let res: Response;
  try {
    res = await fetch(path, {
      method,
      credentials: "include",
      headers,
      body,
      signal: opts.signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") throw error;
    reportConnectivity(false);
    if ((opts.queueWhenOffline || opts.offlineFallback) && mutating && typeof window !== "undefined") {
      return queueOffline(path, opts, method, headers, error);
    }
    const snapshot = await fromSnapshot<T>(path, method, opts);
    if (snapshot) return snapshot.body;
    throw new ApiError(0, "server unreachable", "offline", error);
  }
  reportConnectivity(!UNAVAILABLE.has(res.status));
  if (UNAVAILABLE.has(res.status) && (opts.queueWhenOffline || opts.offlineFallback) && mutating && typeof window !== "undefined") {
    return queueOffline(path, opts, method, headers, new Error(`status ${res.status}`));
  }
  if (UNAVAILABLE.has(res.status)) {
    const snapshot = await fromSnapshot<T>(path, method, opts);
    if (snapshot) return snapshot.body;
  }

  if (res.status === 403 && mutating) {
    const maybe = (await res.clone().json().catch(() => null)) as ErrorBody | null;
    if (maybe?.code === "csrf") {
      clearCsrf();
      const retry = (await ensureCsrf()) ?? readCsrfCookie();
      if (retry && retry !== headers[CSRF_HEADER]) {
        return request<T>(path, opts);
      }
    }
  }

  if (opts.raw) {
    if (!res.ok) {
      throw new ApiError(res.status, res.statusText);
    }
    return res as T;
  }

  const parsed = await parseBody(res);
  if (!res.ok) {
    const err = parsed as ErrorBody | undefined;
    throw new ApiError(
      res.status,
      err?.message || res.statusText || "request failed",
      err?.code,
      parsed,
    );
  }
  if (method === "GET" && isCacheable(path)) {
    void saveSnapshot(path, parsed).catch(() => {});
  }
  return parsed as T;
}

async function fromSnapshot<T>(path: string, method: string, opts: RequestOpts): Promise<{ body: T; savedAt: number } | null> {
  if (method !== "GET" || opts.raw || !isCacheable(path)) return null;
  const snapshot = await loadSnapshot<T>(path).catch(() => null);
  if (snapshot) markStale(snapshot.savedAt);
  return snapshot;
}

export async function head(path: string): Promise<Headers> {
  const res = await fetch(path, { method: "HEAD", credentials: "include", headers: embedHeaders() });
  if (!res.ok) throw new ApiError(res.status, res.statusText);
  return res.headers;
}

export function replayOfflineMutations(): Promise<ReplaySummary> {
  return replayQueuedMutations(async (mutation) => {
    try {
      await request(mutation.path, {
        method: mutation.method,
        body: mutation.body,
        headers: { ...mutation.headers, "Idempotency-Key": mutation.idempotencyKey },
      });
      return { result: "sent" };
    } catch (error) {
      if (error instanceof ApiError) {
        if (error.status === 409) return { result: "conflict", detail: error.body };
        if (error.status === 404 || error.status === 410 || error.status === 400 || error.status === 403) return { result: "dropped" };
      }
      return { result: "failed" };
    }
  });
}

// Connectivity is derived from real request outcomes rather than polling.
// staleSince is the age of the oldest saved snapshot served while degraded.
export type Connectivity = { api: boolean; since: number; staleSince?: number };
let connectivity: Connectivity = { api: true, since: Date.now() };
const connectivityListeners = new Set<(c: Connectivity) => void>();

function publish(next: Connectivity) {
  connectivity = next;
  for (const listener of connectivityListeners) listener(connectivity);
}

export function reportConnectivity(api: boolean) {
  if (connectivity.api === api) return;
  publish({ api, since: Date.now() });
  if (api) {
    stopProbe();
    void replayOfflineMutations().catch(() => {});
  } else {
    scheduleProbe(0);
  }
}

const PROBE_MIN_MS = 2_000;
const PROBE_MAX_MS = 30_000;
let probeTimer: ReturnType<typeof setTimeout> | null = null;
let probeDelay = PROBE_MIN_MS;

function stopProbe() {
  if (probeTimer) clearTimeout(probeTimer);
  probeTimer = null;
  probeDelay = PROBE_MIN_MS;
}

function scheduleProbe(delay: number) {
  if (typeof window === "undefined" || probeTimer) return;
  probeTimer = setTimeout(async () => {
    probeTimer = null;
    if (connectivity.api) return;
    const ok = await fetch("/api/v1/system", { credentials: "include", cache: "no-store" })
      .then((res) => !UNAVAILABLE.has(res.status))
      .catch(() => false);
    if (ok) {
      reportConnectivity(true);
      return;
    }
    probeDelay = Math.min(PROBE_MAX_MS, probeDelay * 2);
    scheduleProbe(probeDelay / 2 + Math.random() * (probeDelay / 2));
  }, delay);
}

if (typeof window !== "undefined") {
  window.addEventListener("online", () => {
    if (!connectivity.api) {
      stopProbe();
      scheduleProbe(0);
    }
  });
}

function markStale(savedAt: number) {
  if (connectivity.api) return;
  if (connectivity.staleSince !== undefined && connectivity.staleSince <= savedAt) return;
  publish({ ...connectivity, staleSince: savedAt });
}

export function getConnectivity(): Connectivity {
  return connectivity;
}

export function onConnectivity(listener: (c: Connectivity) => void): () => void {
  connectivityListeners.add(listener);
  return () => connectivityListeners.delete(listener);
}
