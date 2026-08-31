import { describe, expect, it } from "vitest";
import {
  isVodOnDemand,
  logicalPositionMs,
  seekReplacesSession,
  selectPlaybackEngine,
  sessionOriginMs,
} from "./controller";

describe("playback controller time/session rules", () => {
  it("uses origin 0 for vod_ondemand only", () => {
    expect(isVodOnDemand({ id: "s", delivery: "hls", urls: {}, vod_ondemand: true })).toBe(true);
    expect(sessionOriginMs({ id: "s", delivery: "hls", urls: {}, vod_ondemand: true, seekable_from_ms: 55_000 }, 55_000)).toBe(0);
    expect(sessionOriginMs({ id: "s", delivery: "hls", urls: {}, seekable_from_ms: 12_000 }, 0)).toBe(12_000);
  });

  it("progress is origin + currentTime, never a second seekable offset", () => {
    expect(logicalPositionMs(0, 10)).toBe(10_000);
    expect(logicalPositionMs(55_000, 10)).toBe(65_000);
  });

  it("VOD seek never recreates a session", () => {
    expect(seekReplacesSession(true, false)).toBe(false);
    expect(seekReplacesSession(true, true)).toBe(false);
    expect(seekReplacesSession(false, false)).toBe(true);
    expect(seekReplacesSession(false, true)).toBe(false);
  });

  it("honors session.hls_attach", () => {
    const caps = { hlsJsSupported: true, nativeHls: true };
    expect(selectPlaybackEngine({ id: "s", delivery: "hls", urls: {}, hls_attach: "native" }, caps, false)).toBe("native-hls");
    expect(selectPlaybackEngine({ id: "s", delivery: "hls", urls: {}, hls_attach: "mse" }, caps, true)).toBe("hlsjs");
    expect(selectPlaybackEngine({ id: "s", delivery: "direct", urls: {} }, caps, true)).toBe("direct");
  });

  it("desktop without hls_attach stays hls.js EVENT, never native VOD", () => {
    const caps = { hlsJsSupported: true, nativeHls: true };
    expect(selectPlaybackEngine({ id: "s", delivery: "hls", urls: {} }, caps, false)).toBe("hlsjs");
    expect(isVodOnDemand({ id: "s", delivery: "hls", urls: {}, hls_attach: "mse" })).toBe(false);
    expect(seekReplacesSession(false, false)).toBe(true);
  });
});
