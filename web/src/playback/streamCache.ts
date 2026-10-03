/**
 * Playback buffer cache. While a Jellyfin title plays, the media around the
 * playhead (WINDOW_SEC each side) is fetched into a per-title Cache Storage
 * bucket so a slow source does not stall playback. Fetching pauses with the
 * player. A bucket outlives its player by LINGER_MS so a quick resume reuses
 * it; the janitor then deletes it.
 */

export const WINDOW_SEC = 300;
export const LINGER_MS = 180_000;
export const CACHE_PREFIX = "viewdock-stream-";
export const KEY_PREFIX = "/__stream-cache/";
/** Must match STREAM_CACHE_PROTOCOL in public/sw.js. */
export const STREAM_CACHE_PROTOCOL = 1;

const REGISTRY_KEY = "viewdock:stream-cache";
const HEARTBEAT_MS = 30_000;
const IDLE_POLL_MS = 5_000;
/** Spans this far past the window are kept, so the playhead moving does not evict at once. */
const EVICT_MARGIN_SEC = 30;
/** Backfilling behind the playhead stops when less than this is cached ahead. */
const MIN_AHEAD_SEC = 60;
/** Ahead coverage below WINDOW_SEC minus this switches back to fetching forward. */
const FORWARD_SLACK_SEC = 60;

export function streamCacheSupported(): boolean {
  return typeof caches !== "undefined" && typeof window !== "undefined" && window.isSecureContext;
}

export function streamCacheName(title: string): string {
  return CACHE_PREFIX + title.replace(/[^A-Za-z0-9_-]/g, "_");
}

/** Cache key base for one variant (source, quality, delivery) of a title. */
export function streamKeyBase(variant: string): string {
  return KEY_PREFIX + encodeURIComponent(variant);
}

type Registry = Record<string, { seenAt: number }>;

function readRegistry(): Registry {
  try {
    const raw = localStorage.getItem(REGISTRY_KEY);
    const parsed = raw ? (JSON.parse(raw) as unknown) : null;
    return parsed && typeof parsed === "object" ? (parsed as Registry) : {};
  } catch {
    return {};
  }
}

function writeRegistry(reg: Registry) {
  try {
    localStorage.setItem(REGISTRY_KEY, JSON.stringify(reg));
  } catch {
    /* storage blocked: the janitor treats unknown buckets as abandoned */
  }
}

/** Marks a bucket as in use now. An open player calls this every HEARTBEAT_MS. */
export function touchStreamCache(name: string, now = Date.now()) {
  const reg = readRegistry();
  reg[name] = { seenAt: now };
  writeRegistry(reg);
}

/** Buckets whose player stopped heartbeating more than LINGER_MS ago. */
export function expiredStreamCaches(names: string[], reg: Registry, now: number): string[] {
  return names.filter((name) => {
    if (!name.startsWith(CACHE_PREFIX)) return false;
    const seen = reg[name]?.seenAt;
    return typeof seen !== "number" || now - seen > LINGER_MS;
  });
}

export async function sweepStreamCaches(now = Date.now()): Promise<void> {
  if (typeof caches === "undefined") return;
  const names = await caches.keys();
  const reg = readRegistry();
  const expired = expiredStreamCaches(names, reg, now);
  await Promise.all(expired.map((name) => caches.delete(name)));
  // Drop registry rows whose bucket is gone or was just deleted.
  const live = new Set(names.filter((n) => !expired.includes(n)));
  const fresh = readRegistry();
  let changed = false;
  for (const name of Object.keys(fresh)) {
    if (!live.has(name) && now - fresh[name].seenAt > LINGER_MS) {
      delete fresh[name];
      changed = true;
    }
  }
  if (changed) writeRegistry(fresh);
}

/** Deletes every buffer bucket, for sign-out. */
export async function clearStreamCaches(): Promise<void> {
  if (typeof caches === "undefined") return;
  const names = await caches.keys();
  await Promise.all(names.filter((n) => n.startsWith(CACHE_PREFIX)).map((n) => caches.delete(n)));
  try {
    localStorage.removeItem(REGISTRY_KEY);
  } catch {
    /* ignore */
  }
}

let janitor = 0;

/** Sweeps abandoned buckets now and every 30 seconds while the app is open. */
export function startStreamCacheJanitor() {
  if (janitor || typeof caches === "undefined") return;
  const run = () => void sweepStreamCaches().catch(() => undefined);
  run();
  janitor = window.setInterval(run, 30_000);
}

/** Confirms the active service worker serves direct-play buffer ranges. */
export async function serviceWorkerServesStreamCache(timeoutMs = 500): Promise<boolean> {
  const controller = typeof navigator !== "undefined" ? navigator.serviceWorker?.controller : null;
  if (!controller) return false;
  return new Promise((resolve) => {
    const channel = new MessageChannel();
    const timer = setTimeout(() => resolve(false), timeoutMs);
    channel.port1.onmessage = (event) => {
      clearTimeout(timer);
      resolve((event.data as { streamCache?: number } | null)?.streamCache === STREAM_CACHE_PROTOCOL);
    };
    controller.postMessage({ type: "viewdock:vault-ping" }, [channel.port2]);
  });
}

/** One cacheable piece of the stream (an HLS segment or a byte-range chunk), timed in media seconds. */
export type Span = { key: string; start: number; end: number };

export type Direction = "forward" | "back";

export type WindowPlan = { next: Span | null; dir: Direction; evict: string[] };

/**
 * planWindow picks the next span to fetch and the cached keys to drop.
 * Forward of the playhead comes first. Behind it is filled in ascending
 * order, and the planner keeps going in its last direction while that is
 * safe: transcoding sources restart the encoder whenever requests jump, so
 * alternating directions would restart it on every segment.
 */
export function planWindow(spans: Span[], cached: Set<string>, playhead: number, lastDir: Direction, windowSec = WINDOW_SEC): WindowPlan {
  const lo = playhead - windowSec;
  const hi = playhead + windowSec;
  let forward: Span | null = null;
  let back: Span | null = null;
  for (const s of spans) {
    if (cached.has(s.key)) continue;
    if (s.end > playhead && s.start < hi) {
      if (!forward || s.start < forward.start) forward = s;
    } else if (s.end > lo && s.end <= playhead) {
      if (!back || s.start < back.start) back = s;
    }
  }
  const ahead = forward ? Math.max(0, forward.start - playhead) : windowSec;
  let next: Span | null;
  let dir: Direction;
  if (lastDir === "back" && back && ahead >= MIN_AHEAD_SEC) {
    next = back;
    dir = "back";
  } else if (forward && (lastDir === "forward" || ahead < windowSec - FORWARD_SLACK_SEC || !back)) {
    next = forward;
    dir = "forward";
  } else if (back) {
    next = back;
    dir = "back";
  } else {
    next = forward;
    dir = "forward";
  }

  const evict: string[] = [];
  if (spans.length > 0) {
    const keep = new Set<string>();
    for (const s of spans) {
      if (s.end > lo - EVICT_MARGIN_SEC && s.start < hi + EVICT_MARGIN_SEC) keep.add(s.key);
    }
    for (const key of cached) if (!keep.has(key)) evict.push(key);
  }
  return { next, dir, evict };
}

export class StreamFetchError extends Error {
  status: number;
  constructor(status: number) {
    super(`HTTP ${status}`);
    this.name = "StreamFetchError";
    this.status = status;
  }
}

export type PrefetchSource = {
  /** Spans known now. Direct play lists only the spans near the playhead. */
  spans(playhead: number): Span[];
  /** Fetches one span from the network. A non-2xx response must throw StreamFetchError. */
  fetch(span: Span, signal: AbortSignal): Promise<ArrayBuffer>;
};

type Inflight = { promise: Promise<ArrayBuffer>; abort: AbortController; priority: boolean };

/**
 * StreamPrefetcher keeps one title's buffer window filled. Background
 * fetches run one at a time; a player request for a span that is not
 * cached preempts them, so the source sees a single sequential reader.
 */
export class StreamPrefetcher {
  private video: HTMLVideoElement;
  private name: string;
  private base: string;
  private source: PrefetchSource;
  private onNote?: (what: string, detail?: string) => void;
  private cache: Cache | null = null;
  private cached = new Set<string>();
  private inflight = new Map<string, Inflight>();
  private lastDir: Direction = "forward";
  private closed = false;
  private full = false;
  private failures = 0;
  private wakeUp: (() => void) | null = null;
  private heartbeat = 0;
  private readonly wake = () => {
    this.wakeUp?.();
  };
  private readonly onPause = () => {
    for (const f of this.inflight.values()) if (!f.priority) f.abort.abort();
  };
  private readonly onSeeking = () => {
    this.lastDir = "forward";
    this.wake();
  };

  constructor(opts: {
    video: HTMLVideoElement;
    cacheName: string;
    keyBase: string;
    source: PrefetchSource;
    onNote?: (what: string, detail?: string) => void;
  }) {
    this.video = opts.video;
    this.name = opts.cacheName;
    this.base = opts.keyBase;
    this.source = opts.source;
    this.onNote = opts.onNote;
  }

  async start(): Promise<void> {
    touchStreamCache(this.name);
    this.heartbeat = window.setInterval(() => touchStreamCache(this.name), HEARTBEAT_MS);
    this.video.addEventListener("play", this.wake);
    this.video.addEventListener("pause", this.onPause);
    this.video.addEventListener("seeking", this.onSeeking);
    try {
      const cache = await caches.open(this.name);
      if (this.closed) return;
      const prefix = new URL(this.base + "/", location.origin).href;
      for (const req of await cache.keys()) {
        if (!req.url.startsWith(prefix)) {
          // Another quality or source of the same title.
          void cache.delete(req);
        } else if (!req.url.endsWith("/meta")) {
          this.cached.add(new URL(req.url).pathname);
        }
      }
      this.cache = cache;
      this.onNote?.("stream_cache_open", `${this.name} cached=${this.cached.size}`);
    } catch (err) {
      this.onNote?.("stream_cache_unavailable", String(err));
      return;
    }
    void this.loop();
  }

  /** Wakes the loop, for example after a playlist update. */
  poke() {
    this.wake();
  }

  /** Stores a small JSON record next to the spans (direct play metadata). */
  async putMeta(value: unknown) {
    await this.cache?.put(this.base + "/meta", new Response(JSON.stringify(value), { headers: { "Content-Type": "application/json" } }));
  }

  /** Returns a span for the player: from the cache, the fetch already running, or a priority fetch. */
  async get(span: Span): Promise<ArrayBuffer> {
    if (this.cache && this.cached.has(span.key)) {
      const hit = await this.cache.match(span.key).catch(() => undefined);
      if (hit) return hit.arrayBuffer();
      this.cached.delete(span.key);
    }
    const running = this.inflight.get(span.key);
    if (running) {
      running.priority = true;
      return running.promise;
    }
    for (const f of this.inflight.values()) if (!f.priority) f.abort.abort();
    this.lastDir = "forward";
    const promise = this.fetchStore(span, true);
    promise.finally(this.wake).catch(() => undefined);
    return promise;
  }

  destroy() {
    if (this.closed) return;
    this.closed = true;
    window.clearInterval(this.heartbeat);
    touchStreamCache(this.name);
    this.video.removeEventListener("play", this.wake);
    this.video.removeEventListener("pause", this.onPause);
    this.video.removeEventListener("seeking", this.onSeeking);
    for (const f of this.inflight.values()) f.abort.abort();
    this.wake();
  }

  private fetchStore(span: Span, priority: boolean): Promise<ArrayBuffer> {
    const abort = new AbortController();
    const promise = (async () => {
      const buf = await this.source.fetch(span, abort.signal);
      if (this.cache && !this.closed) {
        try {
          await this.cache.put(span.key, new Response(buf));
          this.cached.add(span.key);
        } catch (err) {
          // Usually QuotaExceededError: keep serving, stop growing until something is evicted.
          if (!this.full) this.onNote?.("stream_cache_full", String(err));
          this.full = true;
        }
      }
      return buf;
    })();
    this.inflight.set(span.key, { promise, abort, priority });
    promise.finally(() => this.inflight.delete(span.key)).catch(() => undefined);
    return promise;
  }

  private sleep(ms = IDLE_POLL_MS): Promise<void> {
    return new Promise((resolve) => {
      const timer = window.setTimeout(done, ms);
      const self = this;
      function done() {
        window.clearTimeout(timer);
        if (self.wakeUp === done) self.wakeUp = null;
        resolve();
      }
      this.wakeUp = done;
    });
  }

  private async loop() {
    while (!this.closed) {
      const cache = this.cache;
      if (!cache || this.video.paused) {
        await this.sleep();
        continue;
      }
      const playhead = this.video.currentTime || 0;
      const plan = planWindow(this.source.spans(playhead), this.cached, playhead, this.lastDir);
      if (plan.evict.length > 0) {
        for (const key of plan.evict) this.cached.delete(key);
        await Promise.all(plan.evict.map((key) => cache.delete(key).catch(() => false)));
        this.full = false;
      }
      if (!plan.next || this.full) {
        await this.sleep();
        continue;
      }
      const running = this.inflight.get(plan.next.key);
      try {
        this.lastDir = plan.dir;
        if (running) await running.promise;
        else await this.fetchStore(plan.next, false);
        this.failures = 0;
      } catch (err) {
        if (this.closed || (err instanceof DOMException && err.name === "AbortError")) continue;
        this.failures++;
        if (this.failures === 1 || this.failures % 10 === 0) this.onNote?.("stream_cache_fetch_failed", String(err));
        if (err instanceof StreamFetchError && err.status === 410) return;
        await this.sleep(Math.min(30_000, 1000 * 2 ** Math.min(this.failures, 5)));
      }
    }
  }
}
