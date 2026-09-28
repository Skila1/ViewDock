import { describe, expect, it } from "vitest";
import { inspectPlaylistBody, playlistReadiness } from "./playlistInspect";

describe("playlistReadiness", () => {
  it("accepts a Jellyfin master playlist that only lists variants", () => {
    const master = [
      "#EXTM3U",
      '#EXT-X-STREAM-INF:BANDWIDTH=8000000,AVERAGE-BANDWIDTH=8000000,CODECS="avc1.640028,mp4a.40.2",RESOLUTION=1920x1080',
      "main.m3u8?DeviceId=viewdock-1&MediaSourceId=abc&PlaySessionId=def",
    ].join("\n");
    expect(playlistReadiness(master)).toBe("master");
  });

  it("accepts a media playlist once it lists segments", () => {
    expect(playlistReadiness("#EXTM3U\n#EXTINF:6.0,\nseg0.ts\n")).toBe("media");
  });

  it("keeps waiting on an empty playlist", () => {
    expect(playlistReadiness("#EXTM3U\n#EXT-X-PLAYLIST-TYPE:EVENT\n")).toBeNull();
  });
});

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
