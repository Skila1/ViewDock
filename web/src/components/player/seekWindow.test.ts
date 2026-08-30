import { describe, expect, it } from "vitest";
import { canSeekInWindow, pinNativeLiveEdge } from "./seekWindow";

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

describe("pinNativeLiveEdge", () => {
  it("snaps back to 0 if Safari jumps to the frontier right after attach", () => {
    expect(
      pinNativeLiveEdge({ relSec: 70, seekableEndSec: 72, attachedAgoMs: 400, lastStableRelSec: 0 }),
    ).toBe(0);
  });

  it("snaps back after a live-edge jump later in playback", () => {
    expect(
      pinNativeLiveEdge({ relSec: 90, seekableEndSec: 92, attachedAgoMs: 60_000, lastStableRelSec: 20 }),
    ).toBe(20);
  });

  it("leaves a normal playhead alone", () => {
    expect(
      pinNativeLiveEdge({ relSec: 12, seekableEndSec: 90, attachedAgoMs: 12_000, lastStableRelSec: 11.7 }),
    ).toBeNull();
  });
});
