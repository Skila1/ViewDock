import { describe, expect, it } from "vitest";
import { seekReplacesSession } from "@/playback/controller";
import { canSeekInWindow, nativeGeneratedEndSec, vodMovieSeekable } from "./seekWindow";

describe("canSeekInWindow", () => {
  it("rejects targets before the session origin", () => {
    expect(canSeekInWindow({ targetMs: 10 * 60_000, originMs: 55 * 60_000, seekableStartSec: 0, seekableEndSec: 70 })).toBe(false);
  });

  it("rejects targets evicted from the back buffer", () => {
    expect(
      canSeekInWindow({
        targetMs: 30_000,
        originMs: 0,
        seekableStartSec: 60,
        seekableEndSec: 120,
      }),
    ).toBe(false);
  });

  it("allows a skip inside the remaining buffer", () => {
    expect(
      canSeekInWindow({
        targetMs: 90_000,
        originMs: 0,
        seekableStartSec: 60,
        seekableEndSec: 120,
      }),
    ).toBe(true);
  });

  it("allows a skip a bit past the frontier", () => {
    expect(
      canSeekInWindow({
        targetMs: 121_000,
        originMs: 0,
        seekableStartSec: 0,
        seekableEndSec: 120,
      }),
    ).toBe(true);
  });

  it("treats an empty seekable range as only the origin", () => {
    expect(canSeekInWindow({ targetMs: 0, originMs: 55 * 60_000 })).toBe(false);
    expect(canSeekInWindow({ targetMs: 55 * 60_000, originMs: 55 * 60_000 })).toBe(true);
  });

  it("rejects a far skip when only a short EVENT window has been generated", () => {
    expect(
      canSeekInWindow({
        targetMs: 409_000,
        originMs: 0,
        seekableStartSec: 0,
        seekableEndSec: 7200,
        generatedEndSec: 30,
      }),
    ).toBe(false);
  });

  it("allows a skip a few seconds past the generated edge", () => {
    expect(
      canSeekInWindow({
        targetMs: 35_000,
        originMs: 0,
        seekableStartSec: 0,
        seekableEndSec: 7200,
        generatedEndSec: 30,
      }),
    ).toBe(true);
  });

  it("does not treat a movie-length iOS seekable range as the EVENT window", () => {
    expect(
      canSeekInWindow({
        targetMs: 1_292_777,
        originMs: 0,
        seekableStartSec: 0,
        seekableEndSec: 10_193,
        ignoreSeekableStart: true,
      }),
    ).toBe(false);
  });

  it("ignores a live-edge seekable start on Apple EVENT playlists", () => {
    expect(
      canSeekInWindow({
        targetMs: 10_000,
        originMs: 0,
        seekableStartSec: 60,
        seekableEndSec: 90,
        ignoreSeekableStart: true,
      }),
    ).toBe(true);
    expect(
      canSeekInWindow({
        targetMs: 10_000,
        originMs: 0,
        seekableStartSec: 60,
        seekableEndSec: 90,
        ignoreSeekableStart: false,
      }),
    ).toBe(false);
  });
});

describe("vodMovieSeekable", () => {
  it("allows forward and back seeks on the same VOD timeline", () => {
    expect(vodMovieSeekable({ targetMs: 3_600_000, durationMs: 10_193_184 })).toBe(true);
    expect(vodMovieSeekable({ targetMs: 10_000, durationMs: 10_193_184 })).toBe(true);
    expect(vodMovieSeekable({ targetMs: 10_193_184, durationMs: 10_193_184 })).toBe(true);
    expect(vodMovieSeekable({ targetMs: -1, durationMs: 10_193_184 })).toBe(false);
  });
});

describe("nativeGeneratedEndSec", () => {
  it("ignores video.duration and a movie-length seekable when the buffer is short", () => {
    const video = {
      duration: 10193,
      buffered: { length: 1, end: () => 12 },
      seekable: { length: 1, end: () => 10193 },
    } as unknown as HTMLVideoElement;
    expect(nativeGeneratedEndSec(video, 14)).toBe(12);
  });

  it("returns undefined when only a movie-length seekable exists", () => {
    const video = {
      duration: 10193,
      buffered: { length: 0, end: () => 0 },
      seekable: { length: 1, end: () => 10193 },
    } as unknown as HTMLVideoElement;
    expect(nativeGeneratedEndSec(video)).toBeUndefined();
  });
});

describe("native VOD must not pin a live edge", () => {
  it("treats every movie timestamp as in-window and never recreates the session", () => {
    expect(vodMovieSeekable({ targetMs: 70_000, durationMs: 10_193_000 })).toBe(true);
    expect(vodMovieSeekable({ targetMs: 3_600_000, durationMs: 10_193_000 })).toBe(true);
    expect(seekReplacesSession(true, false)).toBe(false);
  });
});
