import type Hls from "hls.js";
import type { FragmentLoaderContext, HlsConfig, Loader, LoaderCallbacks, LoaderConfiguration, LoaderContext } from "hls.js";
import {
  KEY_PREFIX,
  StreamFetchError,
  StreamPrefetcher,
  WINDOW_SEC,
  serviceWorkerServesStreamCache,
  streamCacheName,
  streamCacheSupported,
  streamKeyBase,
  type PrefetchSource,
  type Span,
} from "@/playback/streamCache";
import { noteAttach } from "@/playback/attachTrace";
import type { PlaybackSession } from "@/types/api.gen";

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

async function fetchBytes(url: string, signal: AbortSignal, headers?: Record<string, string>, want?: number): Promise<ArrayBuffer> {
  const res = await fetch(url, { credentials: "include", signal, headers });
  if (!res.ok || (want != null && res.status !== want)) throw new StreamFetchError(res.status);
  return res.arrayBuffer();
}

/** Buffer cache for an hls.js session: the prefetcher plus the fragment loader that reads from it. */
export function hlsBufferCache(video: HTMLVideoElement, session: PlaybackSession, title: BufferCacheTitle, HlsCtor: typeof Hls) {
  const base = streamKeyBase(variantOf(session, title));
  let hls: Hls | null = null;
  let level = -1;
  let builtFrom: unknown = null;
  let spans: Span[] = [];
  const urls = new Map<string, string>();

  const keyOf = (lvl: number, sn: number) => `${base}/L${lvl}/${sn}`;
  const source: PrefetchSource = {
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
      return fetchBytes(url, signal);
    },
  };
  const prefetch = new StreamPrefetcher({
    video,
    cacheName: streamCacheName(`${title.kind}-${title.id}`),
    keyBase: base,
    source,
    onNote: (what, detail) => noteAttach(video, what, detail),
  });

  const Base = HlsCtor.DefaultConfig.loader as unknown as new (config: HlsConfig) => Loader<LoaderContext>;
  class CachedFragmentLoader extends Base {
    private cancelled = false;
    private pending: LoaderCallbacks<LoaderContext> | null = null;

    load(context: LoaderContext, config: LoaderConfiguration, callbacks: LoaderCallbacks<LoaderContext>, retries = 0) {
      const { frag, part } = context as FragmentLoaderContext;
      if (!frag || part || typeof frag.sn !== "number" || frag.byteRange.length > 0 || context.rangeStart != null) {
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
    destroy() {
      prefetch.destroy();
    },
  };
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

  spans(playhead: number): Span[] {
    const dur = this.durationSec();
    if (!(this.size > 0) || !(dur > 0)) return [];
    const bps = this.size / dur;
    const last = Math.ceil(this.size / DIRECT_CHUNK) - 1;
    const from = Math.max(0, Math.floor((Math.max(0, playhead - WINDOW_SEC - MARGIN_SEC) * bps) / DIRECT_CHUNK));
    const to = Math.min(last, Math.floor(((playhead + WINDOW_SEC + MARGIN_SEC) * bps) / DIRECT_CHUNK));
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
  return { url, destroy: () => prefetch.destroy() };
}

