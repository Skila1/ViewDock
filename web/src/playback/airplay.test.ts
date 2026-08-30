import { describe, expect, it } from "vitest";
import { addAirPlayAlternate, disableRemotePlaybackForMms, sourceOrder } from "./airplay";

describe("AirPlay alternate source", () => {
  it("disables remote playback before MMS attach", () => {
    const video = document.createElement("video");
    disableRemotePlaybackForMms(video);
    expect(video.disableRemotePlayback).toBe(true);
  });

  it("appends HLS after an existing blob source and re-enables remote playback", () => {
    const video = document.createElement("video");
    disableRemotePlaybackForMms(video);
    const blob = document.createElement("source");
    blob.type = "video/mp4";
    blob.src = "blob:https://example/1";
    video.appendChild(blob);
    addAirPlayAlternate(video, "/hls/s1/index.m3u8?stoken=tok");
    expect(sourceOrder(video)).toEqual(["video/mp4", "application/x-mpegURL"]);
    expect(video.disableRemotePlayback).toBe(false);
    expect(video.getAttribute("x-webkit-airplay")).toBe("allow");
    addAirPlayAlternate(video, "/hls/s1/index.m3u8?stoken=tok");
    expect(video.querySelectorAll("source[data-vd-airplay]").length).toBe(1);
  });
});
