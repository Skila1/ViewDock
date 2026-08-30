import { describe, expect, it } from "vitest";
import { inferDiagnosticOwner, IOS_AVKIT_MMS_VALIDATED, movieDurationMs, selectEngine, withVdDebug } from "./policy";

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

  it("records the validated iPhone AVKit + MMS path and forbids currentTime restore", () => {
    expect(selectEngine("hls", { hlsJsSupported: true, nativeHls: true })).toBe("hlsjs");
    expect(IOS_AVKIT_MMS_VALIDATED.engine).toBe("hls-mms");
    expect(IOS_AVKIT_MMS_VALIDATED.enter).toBe("webkitEnterFullscreen");
    expect(IOS_AVKIT_MMS_VALIDATED.same_blob).toBe(true);
    expect(IOS_AVKIT_MMS_VALIDATED.same_session).toBe(true);
    expect(IOS_AVKIT_MMS_VALIDATED.seek_position_preserved).toBe(true);
    expect(IOS_AVKIT_MMS_VALIDATED.restore_currentTime_on_exit).toBe(false);
  });

  it("appends vd_debug=1 to a watch URL", () => {
    expect(withVdDebug("/watch/movie/x?t=0")).toBe("/watch/movie/x?t=0&vd_debug=1");
    expect(withVdDebug("?t=0")).toBe("?t=0&vd_debug=1");
    expect(withVdDebug("/watch/movie/x?t=0&vd_debug=1")).toBe("/watch/movie/x?t=0&vd_debug=1");
  });

  it("never leaves owner null when hls_attach is mse", () => {
    expect(inferDiagnosticOwner(null, { id: "s", delivery: "hls", urls: {}, hls_attach: "mse" }, null)).toMatch(/^hls-m/);
    expect(inferDiagnosticOwner(null, { id: "s", delivery: "hls", urls: {}, hls_attach: "mse" }, "hlsjs")).toMatch(/^hls-m/);
    expect(inferDiagnosticOwner(null, { id: "s", delivery: "direct", urls: {} }, null)).toBe("direct-file");
    expect(inferDiagnosticOwner(null, { id: "s", delivery: "hls", urls: {}, hls_attach: "native" }, "native-hls")).toBe("native-hls");
  });
});
