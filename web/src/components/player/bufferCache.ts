import type Hls from "hls.js";
import type { FragmentLoaderContext, HlsConfig, Loader, LoaderCallbacks, LoaderConfiguration, LoaderContext } from "hls.js";
import {
  KEY_PREFIX,
  StreamFetchError,
  StreamPrefetcher,
  WINDOW_AHEAD_SEC,
  WINDOW_SEC,
  serviceWorkerServesStreamCache,
  streamCacheName,
  streamCacheSupported,
  streamKeyBase,
  touchStreamCache,
  type PrefetchSource,
  type Span,
  type Window,
} from "@/playback/streamCache";
import { noteAttach } from "@/playback/attachTrace";
import { sessionUrl } from "@/api/profile";
import type { PlaybackSession } from "@/types/api.gen";

export type CacheStats = ReturnType<StreamPrefetcher["stats"]>;

/** What the player knows about the title, for naming its buffer bucket. */
export type BufferCacheTitle = { kind: string; id: string; quality?: string };

const DIRECT_CHUNK = 4 * 1024 * 1024;
const MARGIN_SEC = 30;

/** Only Jellyfin streams are buffered: local files are already close to the server. */
export function bufferCacheEnabled(session: PlaybackSession, title?: BufferCacheTitle): title is BufferCacheTitle {
  return Boolean(title && session.source?.startsWith("jellyfin:") && streamCacheSupported());
}

function variantOf(session: PlaybackSession, title: BufferCacheTitle): string {
  return [session.source ?? "", title.quality ?? "auto", session.delivery, session.seekable_from_ms ?? 0].join("|");
}

/** A download that receives nothing for this long is dropped and requested again. */
const IDLE_ABORT_MS = 15_000;
/**
 * The wait for a response to start is longer: Jellyfin answers only once
 * its remux reaches the segment, which takes a while after a restart, and
 * asking again meanwhile makes it restart once more.
 */
const FIRST_BYTE_MS = 45_000;

async function fetchBytes(url: string, signal: AbortSignal, headers?: Record<string, string>, want?: number): Promise<ArrayBuffer> {
  // A connection can stall without failing; an idle timer turns that into a
  // retryable error instead of a download slot held for a minute.
  const idle = new AbortController();
  const onAbort = () => idle.abort();
  signal.addEventListener("abort", onAbort);
  let timer = window.setTimeout(() => idle.abort(), FIRST_BYTE_MS);
  const kick = () => {
    window.clearTimeout(timer);
    timer = window.setTimeout(() => idle.abort(), IDLE_ABORT_MS);
  };
  try {
    const res = await fetch(url, { credentials: "include", signal: idle.signal, headers });
    if (!res.ok || (want != null && res.status !== want)) throw new StreamFetchError(res.status);
    // Jellyfin can answer 200 with an empty body while it restarts its
    // remux at a new position; that is a failed download to retry, never data.
    if (!res.body) {
      const buf = await res.arrayBuffer();
      if (!buf.byteLength) throw new StreamFetchError(502);
      return buf;
    }
    const reader = res.body.getReader();
    const parts: Uint8Array[] = [];
    let size = 0;
    for (;;) {
      kick();
      const { done, value } = await reader.read();
      if (done) break;
      parts.push(value);
      size += value.byteLength;
    }
    if (size === 0) throw new StreamFetchError(502);
    const out = new Uint8Array(size);
    let at = 0;
    for (const p of parts) {
      out.set(p, at);
      at += p.byteLength;
    }
    return out.buffer;
  } catch (err) {
    // Stalled, not cancelled by the player: report it as a timeout to retry.
    if (idle.signal.aborted && !signal.aborted) throw new StreamFetchError(408);
    throw err;
  } finally {
    window.clearTimeout(timer);
    signal.removeEventListener("abort", onAbort);
  }
}

/**
 * completeSegment rejects a media segment that was cut short. Jellyfin
 * answers 200 with whatever it had written when it kills or restarts a
 * remux, and appending that to the player leaves a hole or garbled audio,
 * so it is a failed download to retry and never stored.
 */
export function completeSegment(buf: ArrayBuffer): ArrayBuffer {
  const bytes = new Uint8Array(buf);
  const view = new DataView(buf);
  // MPEG-TS: whole 188 byte packets.
  if (bytes[0] === 0x47) {
    if (bytes.byteLength % 188 !== 0) throw new StreamFetchError(502);
    return buf;
  }
  // fMP4: top level boxes that end exactly at the end, with media data.
  let at = 0;
  let media = false;
  while (at + 8 <= bytes.byteLength) {
    let size = view.getUint32(at);
    const type = String.fromCharCode(bytes[at + 4], bytes[at + 5], bytes[at + 6], bytes[at + 7]);
    if (size === 1) {
      if (at + 16 > bytes.byteLength) throw new StreamFetchError(502);
      size = view.getUint32(at + 8) * 2 ** 32 + view.getUint32(at + 12);
    } else if (size === 0) {
      size = bytes.byteLength - at;
    }
    if (size < 8 || at + size > bytes.byteLength) throw new StreamFetchError(502);
    if (type === "mdat") media = true;
    at += size;
  }
  if (at !== bytes.byteLength || !media) throw new StreamFetchError(502);
  return buf;
}

/** Buffer cache for an hls.js session: the prefetcher plus the fragment loader that reads from it. */
export function hlsBufferCache(video: HTMLVideoElement, session: PlaybackSession, title: BufferCacheTitle, HlsCtor: typeof Hls, startSec = 0) {
  const base = streamKeyBase(variantOf(session, title));
  let hls: Hls | null = null;
  let level = -1;
  let builtFrom: unknown = null;
  let spans: Span[] = [];
  const urls = new Map<string, string>();

  // Keyed by the variant's codec and height, not hls.js's level index: the
  // index changes with what the browser can play, and the next-episode
  // prefetch must produce the keys the player asks for later.
  const keyOf = (lvl: number, sn: number) => `${base}/${variantTag(hls?.levels[lvl]?.videoCodec, hls?.levels[lvl]?.height)}/${sn}`;
  const source: PrefetchSource = {
    // Jellyfin copying the original video serves any segment at disk speed;
    // a re-encode restarts when requests jump ahead, so it gets one at a time.
    parallel: session.remote_video?.copy ? 2 : 1,
    encodes: !session.remote_video?.copy,
    spans() {
      if (!hls) return [];
      const lvl = level >= 0 ? level : Math.max(0, hls.loadLevel);
      const details = hls.levels[lvl]?.details;
      if (!details) return [];
      if (details !== builtFrom) {
        builtFrom = details;
        spans = [];
        urls.clear();
        for (const frag of details.fragments) {
          if (typeof frag.sn !== "number" || frag.byteRange.length > 0) continue;
          const key = keyOf(lvl, frag.sn);
          urls.set(key, frag.url);
          spans.push({ key, start: frag.start, end: frag.start + frag.duration });
        }
      }
      return spans;
    },
    fetch(span, signal) {
      const url = urls.get(span.key);
      if (!url) return Promise.reject(new StreamFetchError(404));
      return fetchBytes(url, signal).then(completeSegment);
    },
  };
  const prefetch = new StreamPrefetcher({
    video,
    cacheName: streamCacheName(`${title.kind}-${title.id}`),
    keyBase: base,
    source,
    onNote: (what, detail) => noteAttach(video, what, detail),
    startSec,
  });

  const Base = HlsCtor.DefaultConfig.loader as unknown as new (config: HlsConfig) => Loader<LoaderContext>;
  class CachedFragmentLoader extends Base {
    private cancelled = false;
    private pending: LoaderCallbacks<LoaderContext> | null = null;

    load(context: LoaderContext, config: LoaderConfiguration, callbacks: LoaderCallbacks<LoaderContext>, retries = 0) {
      const { frag, part } = context as FragmentLoaderContext;
      // hls.js always sets rangeStart and rangeEnd; both 0 means the whole segment.
      if (!frag || part || typeof frag.sn !== "number" || frag.byteRange.length > 0 || (context.rangeEnd ?? 0) > 0) {
        super.load(context, config, callbacks);
        return;
      }
      level = frag.level;
      source.spans(video.currentTime);
      const key = keyOf(frag.level, frag.sn);
      urls.set(key, frag.url);
      const span = { key, start: frag.start, end: frag.start + frag.duration };
      this.context = context;
      this.pending = callbacks;
      this.cancelled = false;
      const stats = this.stats;
      stats.loading.start = performance.now();
      prefetch.get(span).then(
        (data) => {
          if (this.cancelled) return;
          this.pending = null;
          stats.loading.first = stats.loading.end = performance.now();
          stats.loaded = stats.total = data.byteLength;
          callbacks.onSuccess({ url: context.url, data }, stats, context, null);
        },
        (err: unknown) => {
          if (this.cancelled) return;
          // A shared background fetch was aborted (pause, seek): ask again
          // instead of opening a second request to the source.
          if (err instanceof DOMException && err.name === "AbortError" && retries < 2) {
            this.load(context, config, callbacks, retries + 1);
            return;
          }
          this.pending = null;
          if (err instanceof StreamFetchError) {
            stats.loading.end = performance.now();
            callbacks.onError({ code: err.status, text: err.message }, context, null, stats);
            return;
          }
          // The bucket itself failed: load this fragment the normal way.
          super.load(context, config, callbacks);
        },
      );
    }

    abort() {
      this.cancelled = true;
      const pending = this.pending;
      this.pending = null;
      if (pending && this.context) {
        this.stats.aborted = true;
        pending.onAbort?.(this.stats, this.context, null);
        return;
      }
      super.abort();
    }
  }

  return {
    fLoader: CachedFragmentLoader as unknown as HlsConfig["fLoader"],
    bind(instance: Hls) {
      hls = instance;
      const poke = () => prefetch.poke();
      instance.on(HlsCtor.Events.LEVEL_LOADED, poke);
      instance.on(HlsCtor.Events.LEVEL_UPDATED, poke);
      void prefetch.start();
    },
    prebuffer: (maxMs: number) => prefetch.prebuffer(maxMs, durationSecOf(session, video), (have, want) => noteAttach(video, "prebuffer", `have=${have.toFixed(1)} want=${want}`)),
    stats: () => prefetch.stats(),
    destroy() {
      prefetch.destroy();
    },
  };
}

const CODEC_FAMILIES: Record<string, string> = { avc1: "h264", avc3: "h264", hvc1: "hevc", hev1: "hevc", av01: "av1" };

/** "hevc-2160": a variant's codec family and height, the stable part of a segment's cache key. */
export function variantTag(codecs: string | undefined, height: number | undefined): string {
  const first = (codecs ?? "").split(",").map((c) => c.trim().split(".")[0].toLowerCase()).find((c) => CODEC_FAMILIES[c]);
  return `${first ? CODEC_FAMILIES[first] : "v"}-${height ?? 0}`;
}

type Variant = { uri: string; codecs: string; height: number };

/** Variants of an HLS master playlist, in order. */
export function parseMaster(text: string): Variant[] {
  const lines = text.split(/\r?\n/);
  const out: Variant[] = [];
  for (let i = 0; i < lines.length; i++) {
    if (!lines[i].startsWith("#EXT-X-STREAM-INF:")) continue;
    const attrs = lines[i];
    const codecs = /CODECS="([^"]*)"/.exec(attrs)?.[1] ?? "";
    const height = Number(/RESOLUTION=\d+x(\d+)/.exec(attrs)?.[1] ?? 0);
    const uri = lines.slice(i + 1).find((l) => l && !l.startsWith("#"));
    if (uri) out.push({ uri, codecs, height });
  }
  return out;
}

/** Segments of an HLS media playlist with hls.js's sequence numbers and start times. */
export function parseMedia(text: string): { sn: number; start: number; duration: number; uri: string }[] {
  const lines = text.split(/\r?\n/);
  if (lines.some((l) => l.startsWith("#EXT-X-BYTERANGE"))) return [];
  let sn = Number(/#EXT-X-MEDIA-SEQUENCE:(\d+)/.exec(text)?.[1] ?? 0);
  let start = 0;
  let duration = 0;
  const out: { sn: number; start: number; duration: number; uri: string }[] = [];
  for (const line of lines) {
    if (line.startsWith("#EXTINF:")) duration = parseFloat(line.slice(8));
    else if (line && !line.startsWith("#")) {
      out.push({ sn, start, duration, uri: line });
      start += duration;
      sn++;
      duration = 0;
    }
  }
  return out;
}

/**
 * prefetchOpening stores the first `seconds` of a prepared session's stream
 * in its title's bucket, under the keys its player will ask for, so the
 * next episode starts from local storage. Returns the bytes stored.
 */
export async function prefetchOpening(session: PlaybackSession, title: BufferCacheTitle, seconds: number, signal: AbortSignal): Promise<number> {
  const master = sessionUrl(session.urls, "hls", "playlist", "index", "master");
  if (!master || !bufferCacheEnabled(session, title)) return 0;
  const abs = (uri: string, from: string) => new URL(uri, new URL(from, location.origin)).href;
  const text = await (await fetch(master, { credentials: "include", signal })).text();
  const variants = parseMaster(text);
  const original = session.remote_video?.copy && session.remote_video.codec !== "h264" ? session.remote_video.codec : "";
  const pick = variants.find((v) => !original || variantTag(v.codecs, v.height).startsWith(`${original}-`)) ?? variants[0];
  const mediaUrl = pick ? abs(pick.uri, master) : master;
  const media = parseMedia(await (await fetch(mediaUrl, { credentials: "include", signal })).text());
  const name = streamCacheName(`${title.kind}-${title.id}`);
  const base = streamKeyBase(variantOf(session, title));
  const tag = pick ? variantTag(pick.codecs, pick.height) : "v-0";
  const cache = await caches.open(name);
  let bytes = 0;
  for (const seg of media) {
    if (seg.start >= seconds || signal.aborted) break;
    const buf = completeSegment(await fetchBytes(abs(seg.uri, mediaUrl), signal));
    await cache.put(`${base}/${tag}/${seg.sn}`, new Response(buf, { headers: { "X-VD-Size": String(buf.byteLength) } }));
    bytes += buf.byteLength;
    touchStreamCache(name, Date.now(), bytes);
  }
  return bytes;
}

function durationSecOf(session: PlaybackSession, video: HTMLVideoElement): number {
  if (session.duration_ms && session.duration_ms > 0) return session.duration_ms / 1000;
  return Number.isFinite(video.duration) && video.duration > 0 ? video.duration : Infinity;
}

class DirectSource implements PrefetchSource {
  size = 0;
  private url: string;
  private base: string;
  private durationSec: () => number;

  constructor(url: string, base: string, durationSec: () => number) {
    this.url = url;
    this.base = base;
    this.durationSec = durationSec;
  }

  /** Learns the file size from a one-byte range request. */
  async probe(): Promise<{ size: number; type: string; chunk: number } | null> {
    const res = await fetch(this.url, { credentials: "include", headers: { Range: "bytes=0-0" } });
    const total = /\/(\d+)\s*$/.exec(res.headers.get("Content-Range") ?? "");
    void res.body?.cancel().catch(() => undefined);
    if (res.status !== 206 || !total) return null;
    this.size = Number(total[1]);
    return { size: this.size, type: res.headers.get("Content-Type") || "video/mp4", chunk: DIRECT_CHUNK };
  }

  spans(playhead: number, win?: Window): Span[] {
    const dur = this.durationSec();
    const ahead = win?.ahead ?? WINDOW_AHEAD_SEC;
    const behind = win?.behind ?? WINDOW_SEC;
    if (!(this.size > 0) || !(dur > 0)) return [];
    const bps = this.size / dur;
    const last = Math.ceil(this.size / DIRECT_CHUNK) - 1;
    const from = Math.max(0, Math.floor((Math.max(0, playhead - behind - MARGIN_SEC) * bps) / DIRECT_CHUNK));
    const to = Math.min(last, Math.floor(((playhead + ahead + MARGIN_SEC) * bps) / DIRECT_CHUNK));
    const out: Span[] = [];
    for (let i = from; i <= to; i++) {
      out.push({ key: `${this.base}/${i}`, start: (i * DIRECT_CHUNK) / bps, end: Math.min((i + 1) * DIRECT_CHUNK, this.size) / bps });
    }
    return out;
  }

  fetch(span: Span, signal: AbortSignal): Promise<ArrayBuffer> {
    const i = Number(span.key.slice(span.key.lastIndexOf("/") + 1));
    const start = i * DIRECT_CHUNK;
    const end = Math.min(start + DIRECT_CHUNK, this.size) - 1;
    return fetchBytes(this.url, signal, { Range: `bytes=${start}-${end}` }, 206);
  }
}

/**
 * Direct play: the service worker answers the media element's range
 * requests from the bucket and passes the rest to the network. Returns the
 * URL to give the video element, or null when no capable worker controls the page.
 */
export async function directBufferCache(video: HTMLVideoElement, session: PlaybackSession, title: BufferCacheTitle, src: string) {
  if (!(await serviceWorkerServesStreamCache())) return null;
  const cacheName = streamCacheName(`${title.kind}-${title.id}`);
  const base = streamKeyBase(variantOf(session, title));
  const sessionSec = session.duration_ms && session.duration_ms > 0 ? session.duration_ms / 1000 : 0;
  const source = new DirectSource(src, base, () => (Number.isFinite(video.duration) && video.duration > 0 ? video.duration : sessionSec));
  const prefetch = new StreamPrefetcher({
    video,
    cacheName,
    keyBase: base,
    source,
    onNote: (what, detail) => noteAttach(video, what, detail),
  });
  void (async () => {
    await prefetch.start();
    const meta = await source.probe().catch(() => null);
    if (meta) {
      await prefetch.putMeta(meta).catch(() => undefined);
      prefetch.poke();
    } else {
      noteAttach(video, "stream_cache_no_ranges");
      prefetch.destroy();
    }
  })();
  const url = `${KEY_PREFIX}serve?${new URLSearchParams({ c: cacheName, k: base, u: src })}`;
  return {
    url,
    prebuffer: (maxMs: number) => prefetch.prebuffer(maxMs, durationSecOf(session, video)),
    stats: () => prefetch.stats(),
    destroy: () => prefetch.destroy(),
  };
}

