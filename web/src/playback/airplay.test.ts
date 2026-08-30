import { describe, expect, it } from "vitest";
import { disableRemotePlaybackForMms, stripAlternateSources } from "./airplay";

describe("MMS remote-playback precondition", () => {
  it("disables remote playback before MMS attach", () => {
    const video = document.createElement("video");
    video.setAttribute("x-webkit-airplay", "allow");
    disableRemotePlaybackForMms(video);
    expect(video.disableRemotePlayback).toBe(true);
    expect(video.getAttribute("disableremoteplayback")).toBe("");
    expect(video.hasAttribute("x-webkit-airplay")).toBe(false);
  });

  it("does not add a native HLS sibling that Safari would play inline", () => {
    const video = document.createElement("video");
    const blob = document.createElement("source");
    blob.type = "video/mp4";
    blob.src = "blob:https://example/1";
    video.appendChild(blob);
    disableRemotePlaybackForMms(video);
    expect(video.querySelectorAll("source[data-vd-airplay]").length).toBe(0);
    expect(video.querySelectorAll('source[type="application/x-mpegURL"]').length).toBe(0);
    expect(video.disableRemotePlayback).toBe(true);
  });

  it("strips leftover hls/airplay source children", () => {
    const video = document.createElement("video");
    const air = document.createElement("source");
    air.setAttribute("data-vd-airplay", "1");
    air.type = "application/x-mpegURL";
    video.appendChild(air);
    const hls = document.createElement("source");
    hls.setAttribute("data-vd-hls", "1");
    video.appendChild(hls);
    stripAlternateSources(video);
    expect(video.querySelectorAll("source").length).toBe(0);
  });
});
