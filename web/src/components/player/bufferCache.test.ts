import { afterEach, describe, expect, it, vi } from "vitest";
import type Hls from "hls.js";
import type { HlsConfig, LoaderCallbacks, LoaderConfiguration, LoaderContext } from "hls.js";
import { hlsBufferCache } from "./bufferCache";
import type { PlaybackSession } from "@/types/api.gen";

const network = vi.fn();

class BaseLoader {
  context: LoaderContext | null = null;
  stats = { aborted: false, loaded: 0, retry: 0, total: 0, chunkCount: 0, bwEstimate: 0, loading: { start: 0, first: 0, end: 0 }, parsing: { start: 0, end: 0 }, buffering: { start: 0, first: 0, end: 0 } };
  load() {
    network();
  }
  abort() {}
  destroy() {}
}

const FakeHls = { DefaultConfig: { loader: BaseLoader }, Events: { LEVEL_LOADED: "l", LEVEL_UPDATED: "u" } } as unknown as typeof Hls;

const session = { id: "s", delivery: "hls", urls: {}, source: "jellyfin:m1" } as PlaybackSession;

// The exact shape hls.js's createLoaderContext builds for a whole segment.
function context(sn: number): LoaderContext {
  return {
    type: "media-fragment",
    frag: { sn, level: 0, start: sn * 6, duration: 6, url: `https://x.test/seg/${sn}.mp4`, byteRange: [] },
    part: null,
    responseType: "arraybuffer",
    url: `https://x.test/seg/${sn}.mp4`,
    headers: {},
    rangeStart: 0,
    rangeEnd: 0,
  } as unknown as LoaderContext;
}

afterEach(() => {
  vi.unstubAllGlobals();
  network.mockReset();
});

describe("cached fragment loader", () => {
  it("serves whole segments through the buffer cache, one request per segment", async () => {
    const fetchMock = vi.fn(async () => new Response(new Uint8Array(8)));
    vi.stubGlobal("fetch", fetchMock);
    const video = document.createElement("video");
    const cache = hlsBufferCache(video, session, { kind: "movie", id: "m1" }, FakeHls);
    const Loader = cache.fLoader as unknown as new (c: HlsConfig) => {
      load: (ctx: LoaderContext, cfg: LoaderConfiguration, cb: LoaderCallbacks<LoaderContext>) => void;
    };
    const done: number[] = [];
    const callbacks = (n: number) =>
      ({ onSuccess: () => done.push(n), onError: vi.fn(), onTimeout: vi.fn() }) as unknown as LoaderCallbacks<LoaderContext>;
    // The player and a second consumer ask for the same segment at once.
    new Loader({} as HlsConfig).load(context(3), {} as LoaderConfiguration, callbacks(1));
    new Loader({} as HlsConfig).load(context(3), {} as LoaderConfiguration, callbacks(2));
    await vi.waitFor(() => expect(done).toHaveLength(2));
    expect(network).not.toHaveBeenCalled();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    cache.destroy();
  });

  it("reports an empty segment as a failed download, not as data", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(new Uint8Array(0))));
    const video = document.createElement("video");
    const cache = hlsBufferCache(video, session, { kind: "movie", id: "m1" }, FakeHls);
    const Loader = cache.fLoader as unknown as new (c: HlsConfig) => {
      load: (ctx: LoaderContext, cfg: LoaderConfiguration, cb: LoaderCallbacks<LoaderContext>) => void;
    };
    const onError = vi.fn();
    const onSuccess = vi.fn();
    new Loader({} as HlsConfig).load(context(4), {} as LoaderConfiguration, { onSuccess, onError, onTimeout: vi.fn() } as unknown as LoaderCallbacks<LoaderContext>);
    await vi.waitFor(() => expect(onError).toHaveBeenCalled());
    expect(onError.mock.calls[0][0].code).toBe(502);
    expect(onSuccess).not.toHaveBeenCalled();
    cache.destroy();
  });

  it("leaves real byte-range requests to the default loader", () => {
    const video = document.createElement("video");
    const cache = hlsBufferCache(video, session, { kind: "movie", id: "m1" }, FakeHls);
    const Loader = cache.fLoader as unknown as new (c: HlsConfig) => { load: (...a: unknown[]) => void };
    new Loader({} as HlsConfig).load({ ...context(3), rangeStart: 0, rangeEnd: 1000 }, {}, {});
    expect(network).toHaveBeenCalledTimes(1);
    cache.destroy();
  });
});
