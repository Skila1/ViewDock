import { describe, expect, it } from "vitest";
import { inspectPlaylistBody } from "./playlistInspect";

describe("inspectPlaylistBody", () => {
  it("sums EVENT segments without inventing movie duration", () => {
    const snap = inspectPlaylistBody(
      [
        "#EXTM3U",
        "#EXT-X-PLAYLIST-TYPE:EVENT",
        "#EXT-X-MEDIA-SEQUENCE:0",
        "#EXTINF:6.0,",
        "seg0.ts",
        "#EXTINF:6.0,",
        "seg1.ts",
      ].join("\n"),
      "before",
    );
    expect(snap.type).toBe("EVENT");
    expect(snap.endlist).toBe(false);
    expect(snap.mediaSequence).toBe(0);
    expect(snap.segmentCount).toBe(2);
    expect(snap.sumExtinfSec).toBe(12);
    expect(snap.firstSeg).toBe("seg0.ts");
    expect(snap.lastSeg).toBe("seg1.ts");
  });
});
