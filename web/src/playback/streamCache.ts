/**
 * Playback buffer cache. While a Jellyfin title plays, the media around the
 * playhead (WINDOW_SEC each side) is fetched into a per-title Cache Storage
 * bucket so a slow source does not stall playback. Fetching pauses with the
 * player. A bucket outlives its player by LINGER_MS so a quick resume reuses
 * it; the janitor then deletes it.
 */

/** Kept behind the playhead. */
export const WINDOW_SEC = 300;
/** Downloaded ahead of the playhead, from the moment the session starts. */
export const WINDOW_AHEAD_SEC = 600;
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
const MIN_AHEAD_FOR_BACKFILL_SEC = 60;
/** Ahead coverage below WINDOW_SEC minus this switches back to fetching forward. */
const FORWARD_SLACK_SEC = 60;
/** Disk a bucket may use: fifteen minutes of an original 4K video is about 8 GB. */
const MAX_BUDGET_BYTES = 8 * 1024 ** 3;
const SIZE_HEADER = "X-VD-Size";
/** The ahead window adapts to the title's bitrate between these. */
const MIN_AHEAD_SEC = 300;
const MAX_AHEAD_SEC = 1200;
const LIMITS_KEY = "viewdock:cache-limits";

/** This device's buffer cache limits, in bytes. */
export type CacheLimits = { total: number; perTitle: number };

export const DEFAULT_LIMITS: CacheLimits = { total: 20 * 1024 ** 3, perTitle: MAX_BUDGET_BYTES };

export function cacheLimits(): CacheLimits {
  try {
    const raw = JSON.parse(localStorage.getItem(LIMITS_KEY) ?? "null") as Partial<CacheLimits> | null;
    const total = Number(raw?.total) > 0 ? Number(raw?.total) : DEFAULT_LIMITS.total;
    const perTitle = Number(raw?.perTitle) > 0 ? Number(raw?.perTitle) : DEFAULT_LIMITS.perTitle;
    return { total, perTitle: Math.min(perTitle, total) };
  } catch {
    return DEFAULT_LIMITS;
  }
}

export function setCacheLimits(limits: CacheLimits) {
  try {
    localStorage.setItem(LIMITS_KEY, JSON.stringify(limits));
  } catch {
    /* storage blocked: defaults apply */
  }
}

/**
 * adaptiveWindow sizes the window from the title's measured bitrate and the
 * bucket's budget: a low-bitrate title keeps up to MAX_AHEAD_SEC ahead, a
 * remux what fits in its budget (at least MIN_AHEAD_SEC), and a download
 * slower than playback always asks for the most ahead it can hold.
 */
export function adaptiveWindow(bytesPerSec: number | undefined, budget: number, rate: number | undefined): Window {
  if (!bytesPerSec || bytesPerSec <= 0) return { ahead: WINDOW_AHEAD_SEC, behind: WINDOW_SEC };
  const fits = budget / bytesPerSec;
  let ahead = Math.min(MAX_AHEAD_SEC, Math.max(MIN_AHEAD_SEC, fits * 0.7));
  if (rate != null && rate < 1.2) ahead = Math.max(ahead, Math.min(MAX_AHEAD_SEC, fits * 0.85));
  const behind = Math.min(WINDOW_SEC, Math.max(60, fits - ahead));
  return { ahead: Math.round(ahead), behind: Math.round(behind) };
}

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

type Registry = Record<string, { seenAt: number; bytes?: number }>;

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

/** Marks a bucket as in use now, with its size. An open player calls this every HEARTBEAT_MS. */
export function touchStreamCache(name: string, now = Date.now(), bytes?: number) {
  const reg = readRegistry();
  reg[name] = { seenAt: now, bytes: bytes ?? reg[name]?.bytes };
  writeRegistry(reg);
}

/** Bytes the buffer cache holds on this device, by the players' last reports. */
export function streamCacheUsage(): number {
  return Object.values(readRegistry()).reduce((n, e) => n + (e.bytes ?? 0), 0);
}

/**
 * overTotal picks closed buckets to delete, least recently used first, until
 * the device total fits. Buckets still heartbeating are never picked.
 */
export function overTotal(reg: Registry, total: number, now: number): string[] {
  let used = Object.values(reg).reduce((n, e) => n + (e.bytes ?? 0), 0);
  const out: string[] = [];
  const idle = Object.entries(reg)
    .filter(([, e]) => now - e.seenAt > HEARTBEAT_MS * 2.5)
    .sort((a, b) => a[1].seenAt - b[1].seenAt);
  for (const [name, e] of idle) {
    if (used <= total) break;
    out.push(name);
    used -= e.bytes ?? 0;
  }
  return out;
}

/**
 * behindToEvict picks cached spans furthest behind the playhead (never the
 * last 30 seconds) until bytes fits the budget, so a bucket over budget keeps
 * what is ahead.
 */
export function behindToEvict(spans: Span[], sizes: Map<string, number>, playhead: number, bytes: number, budget: number): string[] {
  const out: string[] = [];
  const behind = spans.filter((s) => sizes.has(s.key) && s.end < playhead - EVICT_MARGIN_SEC).sort((a, b) => a.start - b.start);
  for (const s of behind) {
    if (bytes <= budget) break;
    out.push(s.key);
    bytes -= sizes.get(s.key) ?? 0;
  }
  return out;
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
  const expired = [...new Set([...expiredStreamCaches(names, reg, now), ...overTotal(reg, cacheLimits().total, now)])];
  await Promise.all(expired.map((name) => caches.delete(name)));
  // Drop registry rows whose bucket is gone or was just deleted.
  const live = new Set(names.filter((n) => !expired.includes(n)));
  const fresh = readRegistry();
  let changed = false;
  for (const name of Object.keys(fresh)) {
    if (expired.includes(name) || (!live.has(name) && now - fresh[name].seenAt > LINGER_MS)) {
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

export type Window = { ahead: number; behind: number };

const DEFAULT_WINDOW: Window = { ahead: WINDOW_AHEAD_SEC, behind: WINDOW_SEC };

/**
 * prebufferTarget is how many seconds must be cached ahead before playback
 * starts. rate is media seconds downloaded per second (undefined before the
 * first download): above real time a short head start is enough; below it,
 * enough to finish without stalling, up to the ahead window.
 */
export function prebufferTarget(rate: number | undefined, remainingSec: number): number {
  if (rate == null || rate >= 1.25) return 12;
  if (rate >= 1) return 30;
  const left = Number.isFinite(remainingSec) && remainingSec > 0 ? remainingSec : WINDOW_AHEAD_SEC;
  return Math.min(WINDOW_AHEAD_SEC, Math.round((1 - rate) * left + 30));
}

/** Stored spans merged into [start, end] runs, in media seconds. */
export function cachedRanges(spans: Span[], cached: Set<string>): [number, number][] {
  const out: [number, number][] = [];
  for (const s of [...spans].filter((x) => cached.has(x.key)).sort((a, b) => a.start - b.start)) {
    const last = out[out.length - 1];
    if (last && s.start <= last[1] + 0.5) last[1] = Math.max(last[1], s.end);
    else out.push([s.start, s.end]);
  }
  return out;
}

/** Seconds cached without a gap from the playhead on. */
export function cachedAhead(spans: Span[], cached: Set<string>, playhead: number): number {
  const sorted = spans.filter((s) => s.end > playhead).sort((a, b) => a.start - b.start);
  let edge = playhead;
  for (const s of sorted) {
    if (s.start > edge + 0.5 || !cached.has(s.key)) break;
    edge = s.end;
  }
  return edge - playhead;
}

/**
 * planWindow picks the next span to fetch and the cached keys to drop.
 * Forward of the playhead comes first. Behind it is filled in ascending
 * order, and the planner keeps going in its last direction while that is
 * safe: transcoding sources restart the encoder whenever requests jump, so
 * alternating directions would restart it on every segment.
 */
export function planWindow(
  spans: Span[],
  cached: Set<string>,
  playhead: number,
  lastDir: Direction,
  win: Window = DEFAULT_WINDOW,
  allowBack = true,
): WindowPlan {
  const lo = playhead - win.behind;
  const hi = playhead + win.ahead;
  let forward: Span | null = null;
  let back: Span | null = null;
  for (const s of spans) {
    if (cached.has(s.key)) continue;
    if (s.end > playhead && s.start < hi) {
      if (!forward || s.start < forward.start) forward = s;
    } else if (allowBack && s.end > lo && s.end <= playhead) {
      if (!back || s.start < back.start) back = s;
    }
  }
  const ahead = forward ? Math.max(0, forward.start - playhead) : win.ahead;
  let next: Span | null;
  let dir: Direction;
  if (lastDir === "back" && back && ahead >= MIN_AHEAD_FOR_BACKFILL_SEC) {
    next = back;
    dir = "back";
  } else if (forward && (lastDir === "forward" || ahead < win.ahead - FORWARD_SLACK_SEC || !back)) {
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

/**
 * copyOf hands the player its own buffer: hls.js moves fragment data to its
 * worker, which empties the original that the bucket still stores.
 */
async function copyOf(data: Promise<ArrayBuffer>): Promise<ArrayBuffer> {
  return (await data).slice(0);
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
  /** Spans known now. Direct play lists only the spans inside win around the playhead. */
  spans(playhead: number, win?: Window): Span[];
  /** Fetches one span from the network. A non-2xx response must throw StreamFetchError. */
  fetch(span: Span, signal: AbortSignal): Promise<ArrayBuffer>;
  /**
   * Background downloads at once (default 1). More than one only suits
   * sources that serve any span cheaply: an encoder restarts when requests
   * jump ahead of it.
   */
  parallel?: number;
};

/** promise has the data; stored settles once it is in the bucket (or could not be). */
type Inflight = { promise: Promise<ArrayBuffer>; stored: Promise<void>; abort: AbortController; priority: boolean; span: Span };

/** A player request this far from a background download means a seek: the download is no longer worth its bandwidth. */
const ABORT_DISTANCE_SEC = 120;

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
  /** Where playback will start, until the player is there. */
  private startSec: number;
  private onNote?: (what: string, detail?: string) => void;
  private cache: Cache | null = null;
  private opened: Promise<Cache | null>;
  private markOpened: (cache: Cache | null) => void = () => undefined;
  private cached = new Set<string>();
  private sizes = new Map<string, number>();
  private bytes = 0;
  private budget = MAX_BUDGET_BYTES / 2;
  private inflight = new Map<string, Inflight>();
  private lastDir: Direction = "forward";
  private closed = false;
  private full = false;
  private failures = 0;
  private retryAt = 0;
  private wakeUp: (() => void) | null = null;
  /** Downloads run from the start; only a pause after playback began stops them. */
  private started = false;
  /** Media seconds downloaded per second, averaged. */
  private rate: number | undefined;
  private heartbeat = 0;
  private hits = 0;
  private misses = 0;
  /** Network bytes per second while downloading, averaged. */
  private throughput: number | undefined;
  private readonly wake = () => {
    this.wakeUp?.();
  };

  /**
   * The position to cache around: the video's, or the resume position until
   * playback reaches it, so a resumed title is not fetched from its start.
   */
  private playhead(): number {
    const t = this.video.currentTime || 0;
    return !this.started && t < 0.5 ? this.startSec : t;
  }
  private readonly onPlay = () => {
    this.started = true;
    this.wake();
  };
  // A pause lets downloads already running finish (aborting and fetching
  // them again later would waste them); the loop starts no new ones.
  private readonly onPause = () => undefined;
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
    startSec?: number;
  }) {
    this.startSec = opts.startSec ?? 0;
    this.video = opts.video;
    this.name = opts.cacheName;
    this.base = opts.keyBase;
    this.source = opts.source;
    this.onNote = opts.onNote;
    this.opened = new Promise((resolve) => {
      this.markOpened = resolve;
    });
  }

  async start(): Promise<void> {
    touchStreamCache(this.name);
    this.heartbeat = window.setInterval(() => touchStreamCache(this.name, Date.now(), this.bytes), HEARTBEAT_MS);
    this.video.addEventListener("play", this.onPlay);
    this.video.addEventListener("pause", this.onPause);
    this.video.addEventListener("seeking", this.onSeeking);
    try {
      const estimate = await navigator.storage?.estimate?.().catch(() => undefined);
      const limits = cacheLimits();
      this.budget = Math.min(limits.perTitle, limits.total);
      if (estimate?.quota) this.budget = Math.min(this.budget, estimate.quota * 0.5);
      const cache = await caches.open(this.name);
      if (this.closed) {
        this.markOpened(null);
        return;
      }
      const prefix = new URL(this.base + "/", location.origin).href;
      for (const req of await cache.keys()) {
        if (!req.url.startsWith(prefix)) {
          // Another quality or source of the same title.
          void cache.delete(req);
        } else if (!req.url.endsWith("/meta")) {
          const key = new URL(req.url).pathname;
          const size = Number((await cache.match(req))?.headers.get(SIZE_HEADER)) || 0;
          this.cached.add(key);
          this.sizes.set(key, size);
          this.bytes += size;
        }
      }
      this.cache = cache;
      this.markOpened(cache);
      this.onNote?.("stream_cache_open", `${this.name} cached=${this.cached.size} budget_mb=${Math.round(this.budget / 1e6)}`);
    } catch (err) {
      this.markOpened(null);
      this.onNote?.("stream_cache_unavailable", String(err));
      return;
    }
    void this.loop();
  }

  /** The window for this title now, from its measured bitrate. */
  window(): Window {
    return adaptiveWindow(this.bytesPerMediaSec(), this.budget, this.rate);
  }

  /** Average bytes per second of media among the stored spans. */
  private bytesPerMediaSec(): number | undefined {
    const playhead = this.playhead();
    let bytes = 0;
    let secs = 0;
    for (const s of this.source.spans(playhead)) {
      const size = this.sizes.get(s.key);
      if (size && s.end > s.start) {
        bytes += size;
        secs += s.end - s.start;
      }
    }
    return secs > 0 ? bytes / secs : undefined;
  }

  /** What the stats overlay and the seek bar show. */
  stats(): {
    aheadSec: number;
    behindSec: number;
    bytes: number;
    budget: number;
    hitRate: number | undefined;
    throughputBps: number | undefined;
    rate: number | undefined;
    window: Window;
    ranges: [number, number][];
    mediaBitrateBps: number | undefined;
  } {
    const playhead = this.playhead();
    const spans = this.source.spans(playhead);
    const ranges = cachedRanges(spans, this.cached);
    const around = ranges.find(([a, b]) => a <= playhead + 0.5 && b >= playhead);
    const total = this.hits + this.misses;
    return {
      aheadSec: cachedAhead(spans, this.cached, playhead),
      behindSec: around ? Math.max(0, playhead - around[0]) : 0,
      bytes: this.bytes,
      budget: this.budget,
      hitRate: total ? this.hits / total : undefined,
      throughputBps: this.throughput != null ? this.throughput * 8 : undefined,
      rate: this.rate,
      window: this.window(),
      ranges,
      mediaBitrateBps: (() => {
        const b = this.bytesPerMediaSec();
        return b != null ? b * 8 : undefined;
      })(),
    };
  }

  /** Seconds cached without a gap from the playhead on. */
  aheadSec(): number {
    const playhead = this.playhead();
    return cachedAhead(this.source.spans(playhead), this.cached, playhead);
  }

  /**
   * prebuffer resolves once enough is cached ahead to play without stalling
   * (prebufferTarget), or after maxMs. The player waits on it before it
   * starts playback.
   */
  async prebuffer(maxMs: number, durationSec: number, onProgress?: (have: number, want: number) => void): Promise<void> {
    const deadline = Date.now() + maxMs;
    while (!this.closed && Date.now() < deadline) {
      const playhead = this.playhead();
      const want = prebufferTarget(this.rate, durationSec - playhead);
      const have = this.aheadSec();
      onProgress?.(have, want);
      if (have >= want || playhead + have >= durationSec - 1) return;
      this.wake();
      await new Promise((r) => setTimeout(r, 250));
    }
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
      if (hit) {
        this.hits++;
        return hit.arrayBuffer();
      }
      this.cached.delete(span.key);
    }
    this.misses++;
    const running = this.inflight.get(span.key);
    if (running) {
      running.priority = true;
      return copyOf(running.promise);
    }
    // A source that re-encodes restarts its encoder for every jump, so it
    // keeps a single reader: whatever runs in the background stops. A source
    // copying the video only loses downloads far from what the player needs.
    const single = (this.source.parallel ?? 1) <= 1;
    for (const f of this.inflight.values()) {
      if (!f.priority && (single || Math.abs(f.span.start - span.start) > ABORT_DISTANCE_SEC)) f.abort.abort();
    }
    this.lastDir = "forward";
    const job = this.fetchStore(span, true);
    job.stored.finally(this.wake).catch(() => undefined);
    return copyOf(job.promise);
  }

  destroy() {
    if (this.closed) return;
    this.closed = true;
    window.clearInterval(this.heartbeat);
    touchStreamCache(this.name);
    this.video.removeEventListener("play", this.onPlay);
    this.video.removeEventListener("pause", this.onPause);
    this.video.removeEventListener("seeking", this.onSeeking);
    for (const f of this.inflight.values()) f.abort.abort();
    this.markOpened(null);
    this.wake();
  }

  private fetchStore(span: Span, priority: boolean): Inflight {
    const abort = new AbortController();
    // Parallel downloads share the connection, so one download's speed
    // times how many ran is the overall rate.
    const sharing = this.inflight.size + 1;
    const promise = (async () => {
      const began = performance.now();
      const buf = await this.source.fetch(span, abort.signal);
      const took = (performance.now() - began) / 1000;
      if (took > 0.05 && span.end > span.start) {
        const sample = ((span.end - span.start) / took) * sharing;
        this.rate = this.rate == null ? sample : this.rate * 0.7 + sample * 0.3;
        const bytes = (buf.byteLength / took) * sharing;
        this.throughput = this.throughput == null ? bytes : this.throughput * 0.7 + bytes * 0.3;
      }
      return buf;
    })();
    // The player gets the data at once; storing never delays playback. The
    // span stays in flight until stored, so nothing downloads it twice.
    const stored = promise.then(async (buf) => {
      const cache = await this.opened;
      if (!cache || this.closed || this.cached.has(span.key)) return;
      try {
        await cache.put(span.key, new Response(buf, { headers: { [SIZE_HEADER]: String(buf.byteLength) } }));
        this.cached.add(span.key);
        this.sizes.set(span.key, buf.byteLength);
        this.bytes += buf.byteLength;
      } catch (err) {
        // Usually QuotaExceededError: keep serving, stop growing until something is evicted.
        if (!this.full) this.onNote?.("stream_cache_full", String(err));
        this.full = true;
      }
    });
    const job: Inflight = { promise, stored, abort, priority, span };
    this.inflight.set(span.key, job);
    stored.finally(() => this.inflight.delete(span.key)).catch(() => undefined);
    return job;
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
      if (!cache || (this.started && this.video.paused)) {
        await this.sleep();
        continue;
      }
      if (Date.now() < this.retryAt) {
        await this.sleep(this.retryAt - Date.now());
        continue;
      }
      const playhead = this.playhead();
      const win = this.window();
      const spans = this.source.spans(playhead, win);
      // Spans already downloading count as taken when picking the next one.
      const taken = () => new Set([...this.cached, ...this.inflight.keys()]);
      let plan = planWindow(spans, taken(), playhead, this.lastDir, win);
      const evict = plan.evict.filter((key) => this.cached.has(key));
      if (this.bytes >= this.budget) evict.push(...behindToEvict(spans, this.sizes, playhead, this.bytes, this.budget * 0.9));
      if (evict.length > 0) {
        for (const key of evict) {
          this.cached.delete(key);
          this.bytes -= this.sizes.get(key) ?? 0;
          this.sizes.delete(key);
        }
        await Promise.all(evict.map((key) => cache.delete(key).catch(() => false)));
        this.full = false;
        plan = planWindow(spans, taken(), playhead, this.lastDir, win);
      }
      // Near the budget only what is ahead is fetched.
      if (this.bytes >= this.budget * 0.8) plan = planWindow(spans, taken(), playhead, "forward", win, false);
      if (this.bytes >= this.budget) plan = { ...plan, next: null };
      const background = [...this.inflight.values()].filter((f) => !f.priority);
      if (!plan.next || this.full || background.length >= (this.source.parallel ?? 1)) {
        // Wait for a download to finish or for something to change.
        await Promise.race([this.sleep(), ...background.map((f) => f.stored.catch(() => undefined))]);
        continue;
      }
      this.lastDir = plan.dir;
      const job = this.fetchStore(plan.next, false);
      job.stored.then(
        () => {
          this.failures = 0;
        },
        (err: unknown) => {
          if (this.closed || (err instanceof DOMException && err.name === "AbortError")) return;
          this.failures++;
          if (this.failures === 1 || this.failures % 10 === 0) this.onNote?.("stream_cache_fetch_failed", String(err));
          // 410: the session ended, nothing more to download.
          this.retryAt = err instanceof StreamFetchError && err.status === 410
            ? Number.POSITIVE_INFINITY
            : Date.now() + Math.min(30_000, 1000 * 2 ** Math.min(this.failures, 5));
          this.wake();
        },
      );
    }
  }
}
