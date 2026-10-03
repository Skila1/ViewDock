import { describe, expect, it } from "vitest";
import { LINGER_MS, behindToEvict, expiredStreamCaches, planWindow, streamCacheName, type Span } from "./streamCache";

// 6 second segments covering a 20 minute title.
const spans: Span[] = Array.from({ length: 200 }, (_, i) => ({ key: `s${i}`, start: i * 6, end: (i + 1) * 6 }));
const range = (from: number, to: number) => new Set(spans.slice(from, to).map((s) => s.key));

describe("planWindow", () => {
  it("fetches the segment under the playhead first", () => {
    const plan = planWindow(spans, new Set(), 600, "forward");
    expect(plan.next?.key).toBe("s100");
    expect(plan.dir).toBe("forward");
  });

  it("keeps filling forward until five minutes ahead are cached", () => {
    const plan = planWindow(spans, range(100, 130), 600, "forward");
    expect(plan.next?.key).toBe("s130");
  });

  it("backfills behind in ascending order once ahead is full", () => {
    const plan = planWindow(spans, range(100, 150), 600, "forward");
    expect(plan.next?.key).toBe("s50");
    expect(plan.dir).toBe("back");
  });

  it("stays on the backfill while enough is cached ahead", () => {
    // The playhead moved one segment, opening a gap at the forward edge.
    const cached = new Set([...range(60, 100), ...range(101, 150)]);
    const plan = planWindow(spans, cached, 606, "back");
    expect(plan.next?.key).toBe("s51");
    expect(plan.dir).toBe("back");
  });

  it("returns forward when the ahead buffer runs low", () => {
    const cached = new Set([...range(60, 100), ...range(100, 105)]);
    const plan = planWindow(spans, cached, 600, "back");
    expect(plan.next?.key).toBe("s105");
    expect(plan.dir).toBe("forward");
  });

  it("evicts spans outside the window and its margin", () => {
    const plan = planWindow(spans, new Set(["s0", "s45", "s100", "s154", "s155", "s199"]), 600, "forward");
    expect(plan.evict.sort()).toEqual(["s0", "s155", "s199"]);
  });

  it("does not evict before the playlist is known", () => {
    expect(planWindow([], new Set(["s1"]), 600, "forward").evict).toEqual([]);
  });

  it("skips the backfill near the disk budget", () => {
    const plan = planWindow(spans, range(100, 150), 600, "forward", 300, false);
    expect(plan.next).toBeNull();
  });

  it("has nothing to do when the window is cached", () => {
    expect(planWindow(spans, range(50, 150), 600, "forward").next).toBeNull();
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
