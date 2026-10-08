import { describe, expect, it } from "vitest";
import { buildPlaybackErrorReport, mediaErrorMessage, scrubDiagnostic } from "./errorReport";
import type { PlaybackSession } from "@/types/api.gen";

describe("scrubDiagnostic", () => {
  it("removes stream grants, stream tokens and playlist queries", () => {
    const out = scrubDiagnostic(
      "GET /api/v1/media-sources/stream/abcDEF123_-/Videos/1/master.m3u8?PlaySessionId=x&api_key=k seg=/hls/s/seg1.ts?stoken=zz",
    );
    expect(out).not.toContain("abcDEF123_-");
    expect(out).not.toContain("PlaySessionId=x");
    expect(out).not.toContain("stoken=zz");
    expect(out).toContain("/media-sources/stream/[redacted]/Videos/1/master.m3u8?[query]");
  });
});

describe("buildPlaybackErrorReport", () => {
  it("builds matching text and structured context", () => {
    const session = {
      id: "sess1",
      delivery: "hls",
      decision: { mode: "remote", playback: "transcode" },
    } as unknown as PlaybackSession;
    const r = buildPlaybackErrorReport({
      message: "Stream is still starting. Retry in a moment.",
      code: "ATTACH_FAILED",
      stage: "startup",
      itemKind: "movie",
      itemId: "m1",
      title: "2 Fast 2 Furious",
      session,
      engine: "hlsjs",
      video: { readyState: 0, networkState: 2, currentTime: 0, duration: NaN, paused: true, error: { code: 2, message: "" } },
      trace: {
        events: [{ t: 0, ev: "session_created", detail: "id=sess1" }],
        hlsErrors: [{ t: 0, type: "networkError", details: "manifestLoadError", fatal: true, code: 502 }],
      },
      page: "/watch/movie/m1",
      mse: true,
      nativeHls: false,
      now: Date.UTC(2026, 8, 29),
    });
    expect(r.text).toContain("Code: ATTACH_FAILED");
    expect(r.text).toContain("Session: sess1");
    expect(r.text).toContain("Mode: remote, delivery: hls");
    expect(r.text).toContain("error=MEDIA_ERR_NETWORK");
    expect(r.text).toContain("manifestLoadError | fatal | http 502");
    expect(r.text).toContain("session_created id=sess1");
    expect(r.request).toMatchObject({
      code: "ATTACH_FAILED",
      stage: "startup",
      context: { item_id: "m1", session_id: "sess1", mode: "remote", delivery: "hls", engine: "hlsjs", ready_state: 0, mse: true },
    });
    expect(r.request.trace).toBe(r.text);
  });

  it("describes media errors for viewers", () => {
    expect(mediaErrorMessage(3)).toMatch(/decode/);
    expect(mediaErrorMessage(99)).toMatch(/media error/);
  });
});
