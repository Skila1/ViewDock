import { describe, expect, it } from "vitest";
import { eventPlaylistHlsSync, liveSyncWouldSeek } from "./hlsLiveSync";

describe("eventPlaylistHlsSync", () => {
  it("never treats EVENT growth as a live-latency seek", () => {
    const cfg = eventPlaylistHlsSync();
    expect(cfg.liveMaxLatencyDurationCount).toBe(Number.POSITIVE_INFINITY);
    expect(cfg.liveMaxLatencyDurationCount).toBeGreaterThan(cfg.liveSyncDurationCount);
    expect(
      liveSyncWouldSeek({
        currentTime: 41,
        playlistEdgeSec: 92,
        targetDurationSec: 2,
        liveMaxLatencyDurationCount: cfg.liveMaxLatencyDurationCount,
      }),
    ).toBe(false);
  });

  it("reproduces the confirmed skip: liveMaxLatencyDurationCount=3 seeks to the edge", () => {
    expect(
      liveSyncWouldSeek({
        currentTime: 41,
        playlistEdgeSec: 92,
        targetDurationSec: 2,
        liveMaxLatencyDurationCount: 3,
      }),
    ).toBe(true);
  });
});
