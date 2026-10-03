import { describe, expect, it, vi } from "vitest";
import { LINGER_MS, adaptiveWindow, behindToEvict, cachedAhead, cachedRanges, expiredStreamCaches, overTotal, planWindow, prebufferTarget, streamCacheName, StreamPrefetcher, type PrefetchSource, type Span } from "./streamCache";

// 6 second segments covering a 20 minute title.
const spans: Span[] = Array.from({ length: 200 }, (_, i) => ({ key: `s${i}`, start: i * 6, end: (i + 1) * 6 }));
const range = (from: number, to: number) => new Set(spans.slice(from, to).map((s) => s.key));

const FIVE = { ahead: 300, behind: 300 };

describe("planWindow", () => {
  it("fetches the segment under the playhead first", () => {
    const plan = planWindow(spans, new Set(), 600, "forward", FIVE);
    expect(plan.next?.key).toBe("s100");
    expect(plan.dir).toBe("forward");
  });

  it("keeps filling forward until five minutes ahead are cached", () => {
    const plan = planWindow(spans, range(100, 130), 600, "forward", FIVE);
    expect(plan.next?.key).toBe("s130");
  });

  it("backfills behind in ascending order once ahead is full", () => {
    const plan = planWindow(spans, range(100, 150), 600, "forward", FIVE);
    expect(plan.next?.key).toBe("s50");
    expect(plan.dir).toBe("back");
  });

  it("stays on the backfill while enough is cached ahead", () => {
    // The playhead moved one segment, opening a gap at the forward edge.
    const cached = new Set([...range(60, 100), ...range(101, 150)]);
    const plan = planWindow(spans, cached, 606, "back", FIVE);
    expect(plan.next?.key).toBe("s51");
    expect(plan.dir).toBe("back");
  });

  it("returns forward when the ahead buffer runs low", () => {
    const cached = new Set([...range(60, 100), ...range(100, 105)]);
    const plan = planWindow(spans, cached, 600, "back", FIVE);
    expect(plan.next?.key).toBe("s105");
    expect(plan.dir).toBe("forward");
  });

  it("evicts spans outside the window and its margin", () => {
    const plan = planWindow(spans, new Set(["s0", "s45", "s100", "s154", "s155", "s199"]), 600, "forward", FIVE);
    expect(plan.evict.sort()).toEqual(["s0", "s155", "s199"]);
  });

  it("does not evict before the playlist is known", () => {
    expect(planWindow([], new Set(["s1"]), 600, "forward").evict).toEqual([]);
  });

  it("skips the backfill near the disk budget", () => {
    const plan = planWindow(spans, range(100, 150), 600, "forward", FIVE, false);
    expect(plan.next).toBeNull();
  });

  it("has nothing to do when the window is cached", () => {
    expect(planWindow(spans, range(50, 150), 600, "forward", FIVE).next).toBeNull();
  });

  it("downloads ten minutes ahead by default", () => {
    expect(planWindow(spans, range(100, 150), 600, "forward").next?.key).toBe("s150");
    expect(planWindow(spans, range(100, 200), 600, "forward").next?.key).toBe("s50");
  });
});

describe("prebuffer", () => {
  it("needs a short head start when downloads outrun playback", () => {
    expect(prebufferTarget(undefined, 6000)).toBe(12);
    expect(prebufferTarget(2, 6000)).toBe(12);
    expect(prebufferTarget(1.1, 6000)).toBe(30);
  });

  it("buffers enough to finish when downloads are slower than playback", () => {
    expect(prebufferTarget(0.95, 300)).toBe(45);
    expect(prebufferTarget(0.5, 6000)).toBe(600);
  });

  it("measures gapless cached seconds from the playhead", () => {
    expect(cachedAhead(spans, range(100, 110), 603)).toBe(57);
    expect(cachedAhead(spans, new Set([...range(100, 103), ...range(104, 110)]), 600)).toBe(18);
    expect(cachedAhead(spans, range(101, 110), 600)).toBe(0);
  });
});

describe("behindToEvict", () => {
  it("drops the oldest spans behind the playhead until the budget fits", () => {
    const sizes = new Map([...range(50, 110)].map((k) => [k, 10] as [string, number]));
    // 60 spans of 10 bytes, budget 450: drop 15, oldest first, never the 30s behind the playhead.
    const out = behindToEvict(spans, sizes, 600, 600, 450);
    expect(out).toEqual(Array.from({ length: 15 }, (_, i) => `s${50 + i}`));
  });

  it("keeps the last 30 seconds even when over budget", () => {
    const sizes = new Map([...range(96, 110)].map((k) => [k, 10] as [string, number]));
    expect(behindToEvict(spans, sizes, 600, 140, 0)).toEqual([]);
  });
});

describe("expiredStreamCaches", () => {
  const now = 10_000_000;
  it("keeps buckets with a recent heartbeat and drops abandoned ones", () => {
    const names = ["viewdock-stream-movie-a", "viewdock-stream-movie-b", "viewdock-stream-movie-c", "viewdock-artwork-v1"];
    const reg = {
      "viewdock-stream-movie-a": { seenAt: now - 60_000 },
      "viewdock-stream-movie-b": { seenAt: now - LINGER_MS - 1 },
    };
    expect(expiredStreamCaches(names, reg, now)).toEqual(["viewdock-stream-movie-b", "viewdock-stream-movie-c"]);
  });
});

describe("streamCacheName", () => {
  it("keeps names to safe characters", () => {
    expect(streamCacheName("movie-ab/c d")).toBe("viewdock-stream-movie-ab_c_d");
  });
});

describe("adaptiveWindow", () => {
  const GB = 1024 ** 3;
  it("keeps more ahead for a low bitrate title and what fits for a remux", () => {
    expect(adaptiveWindow(500_000, 8 * GB, 3).ahead).toBe(1200); // 4 Mbps
    const remux = adaptiveWindow(10_000_000, 8 * GB, 2); // 80 Mbps
    expect(remux.ahead).toBeGreaterThanOrEqual(300);
    expect(remux.ahead).toBeLessThan(700);
    expect(remux.ahead + remux.behind).toBeLessThanOrEqual((8 * GB) / 10_000_000 + 1);
  });
  it("asks for more ahead when downloads barely keep up", () => {
    expect(adaptiveWindow(10_000_000, 8 * GB, 1.05).ahead).toBeGreaterThan(adaptiveWindow(10_000_000, 8 * GB, 3).ahead);
  });
  it("uses the default window before the bitrate is known", () => {
    expect(adaptiveWindow(undefined, 8 * GB, undefined)).toEqual({ ahead: 600, behind: 300 });
  });
});

describe("overTotal", () => {
  it("evicts idle buckets least recently used first, never active ones", () => {
    const now = 10_000_000;
    const reg = {
      a: { seenAt: now - 600_000, bytes: 4 },
      b: { seenAt: now - 300_000, bytes: 4 },
      live: { seenAt: now - 5_000, bytes: 8 },
    };
    expect(overTotal(reg, 12, now)).toEqual(["a"]);
    expect(overTotal(reg, 4, now)).toEqual(["a", "b"]);
  });
});

describe("cachedRanges", () => {
  it("merges adjacent stored spans", () => {
    expect(cachedRanges(spans, new Set([...range(0, 3), ...range(5, 7)]))).toEqual([
      [0, 18],
      [30, 42],
    ]);
  });
});

describe("StreamPrefetcher", () => {
  it("runs one download until the source answers, then the allowed parallel downloads", async () => {
    const store = new Map<string, Response>();
    const bucket = {
      keys: async () => [],
      match: async (k: string) => store.get(k),
      put: async (k: string, r: Response) => void store.set(k, r),
      delete: async (k: string) => store.delete(k),
    };
    vi.stubGlobal("caches", { open: async () => bucket, keys: async () => [], delete: async () => true });
    const video = document.createElement("video");
    Object.defineProperty(video, "paused", { value: false });
    const pending: (() => void)[] = [];
    let running = 0;
    let most = 0;
    const source: PrefetchSource = {
      parallel: 2,
      spans: () => spans,
      fetch: () => {
        running++;
        most = Math.max(most, running);
        return new Promise<ArrayBuffer>((resolve) =>
          pending.push(() => {
            running--;
            resolve(new ArrayBuffer(4));
          }),
        );
      },
    };
    const p = new StreamPrefetcher({ video, cacheName: "vd-test", keyBase: "/k", source });
    await p.start();
    await vi.waitFor(() => expect(pending).toHaveLength(1));
    await new Promise((r) => setTimeout(r, 50));
    expect(most).toBe(1);
    pending.shift()!();
    await vi.waitFor(() => expect(running).toBe(2));
    p.destroy();
    vi.unstubAllGlobals();
  });

  it("never fetches behind the playhead from a source that encodes", async () => {
    const bucket = { keys: async () => [], match: async () => undefined, put: async () => undefined, delete: async () => true };
    vi.stubGlobal("caches", { open: async () => bucket, keys: async () => [], delete: async () => true });
    const video = document.createElement("video");
    Object.defineProperty(video, "paused", { value: false });
    Object.defineProperty(video, "currentTime", { value: 300 });
    const asked: number[] = [];
    const source: PrefetchSource = {
      encodes: true,
      spans: () => spans,
      fetch: async (span) => {
        asked.push(span.start);
        return new ArrayBuffer(4);
      },
    };
    const p = new StreamPrefetcher({ video, cacheName: "vd-test", keyBase: "/k", source, startSec: 300 });
    await p.start();
    await vi.waitFor(() => expect(asked.length).toBeGreaterThan(20));
    p.destroy();
    vi.unstubAllGlobals();
    expect(asked.every((start) => start >= 300)).toBe(true);
  });
});
