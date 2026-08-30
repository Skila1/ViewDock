import { describe, expect, it } from "vitest";
import { movieDurationMs, selectEngine } from "./policy";

describe("playback policy", () => {
  it("prefers hls.js over native HLS whenever MSE/MMS is available", () => {
    expect(selectEngine("hls", { hlsJsSupported: true, nativeHls: true })).toBe("hlsjs");
    expect(selectEngine("hls", { hlsJsSupported: false, nativeHls: true })).toBe("native-hls");
    expect(selectEngine("direct", { hlsJsSupported: true, nativeHls: true })).toBe("direct");
  });

  it("uses session duration_ms, not a 30s HLS window", () => {
    expect(movieDurationMs({ id: "s", delivery: "hls", urls: {}, duration_ms: 7_200_000 }, 32_000)).toBe(7_200_000);
    expect(movieDurationMs({ id: "s", delivery: "hls", urls: {} }, 32_000)).toBe(32_000);
    expect(movieDurationMs({ id: "s", delivery: "hls", urls: {} }, Number.POSITIVE_INFINITY)).toBe(0);
  });
});
