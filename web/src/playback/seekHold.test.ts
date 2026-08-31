import { describe, expect, it } from "vitest";
import { captureSeekHold, seekHoldAction, shouldReplaceForGenerated } from "./seekHold";

describe("seekHoldAction", () => {
  it("holds a seek past the EVENT edge instead of accepting live-sync snapback", () => {
    expect(
      seekHoldAction({
        requestedSec: 7040,
        nowSec: 5535,
        playlistEdgeSec: 5835,
        movieSec: 10193,
        endlist: false,
        heldForMs: 2000,
      }),
    ).toBe("keep");
  });

  it("applies the hold once remux has generated that time", () => {
    expect(
      seekHoldAction({
        requestedSec: 7040,
        nowSec: 5835,
        playlistEdgeSec: 7040,
        movieSec: 10193,
        endlist: false,
        heldForMs: 8000,
      }),
    ).toBe("apply");
  });

  it("replaces the session when the seek is far past generated media", () => {
    expect(shouldReplaceForGenerated(409, 30)).toBe(true);
    expect(shouldReplaceForGenerated(35, 30)).toBe(false);
  });

  it("captures an AVKit seek past the current playlist edge", () => {
    expect(captureSeekHold(6000, 1274, 10193)).toBe(6000);
    expect(captureSeekHold(1000, 1274, 10193)).toBeNull();
    expect(captureSeekHold(11000, 1274, 10193)).toBeNull();
  });
});
