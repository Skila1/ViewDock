import { describe, expect, it, vi, afterEach } from "vitest";
import { allowInlinePlayback, enterAvkitFromUserGesture, enterAvkitDetailed, enterNativeFullscreen, isAppleWebKitPlayer, isIOSDevice, isIPhone } from "./device";

describe("device", () => {
  it("detects iPhone and iPad", () => {
    expect(isIOSDevice("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)")).toBe(true);
    expect(isIOSDevice("Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)")).toBe(true);
    expect(isIOSDevice("Mozilla/5.0 (Windows NT 10.0; Win64; x64)")).toBe(false);
    expect(isIPhone("Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X)")).toBe(true);
    expect(isIPhone("Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)")).toBe(false);
  });

  it("detects iPadOS desktop UA", () => {
    expect(isIOSDevice("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", "MacIntel", 5)).toBe(true);
    expect(isIOSDevice("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)", "MacIntel", 0)).toBe(false);
  });

  it("treats iOS Chrome as Apple WebKit player", () => {
    const ua =
      "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/120.0.0.0 Mobile/15E148 Safari/604.1";
    expect(isAppleWebKitPlayer(ua)).toBe(true);
  });

  it("treats desktop Safari as Apple player, not Chrome", () => {
    expect(
      isAppleWebKitPlayer(
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
      ),
    ).toBe(true);
    expect(
      isAppleWebKitPlayer(
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
      ),
    ).toBe(false);
  });

  it("opens iPhone AVKit via webkitEnterFullscreen, not presentation mode", () => {
    vi.stubGlobal("navigator", {
      userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)",
      platform: "iPhone",
      maxTouchPoints: 5,
    });
    const enter = vi.fn();
    const present = vi.fn();
    const pause = vi.fn();
    const play = vi.fn().mockResolvedValue(undefined);
    const video = {
      paused: false,
      play,
      pause,
      disableRemotePlayback: true,
      webkitEnterFullscreen: enter,
      webkitSetPresentationMode: present,
      webkitSupportsFullscreen: true,
      removeAttribute: vi.fn(),
    } as unknown as HTMLVideoElement;
    expect(enterNativeFullscreen(video)).toBe(true);
    expect(video.disableRemotePlayback).toBe(true);
    expect(enter).toHaveBeenCalledOnce();
    expect(pause).toHaveBeenCalledOnce();
    expect(play).toHaveBeenCalledOnce();
    expect(present).not.toHaveBeenCalled();
  });

  it("starts playback before AVKit when the video is paused", () => {
    vi.stubGlobal("navigator", {
      userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X)",
      platform: "iPhone",
      maxTouchPoints: 5,
    });
    const enter = vi.fn();
    const play = vi.fn().mockResolvedValue(undefined);
    const video = {
      paused: true,
      play,
      disableRemotePlayback: true,
      webkitEnterFullscreen: enter,
      removeAttribute: vi.fn(),
    } as unknown as HTMLVideoElement;
    expect(enterAvkitFromUserGesture(video)).toBe(true);
    expect(play).toHaveBeenCalledOnce();
    expect(enter).toHaveBeenCalledOnce();
  });

  it("strips playsinline before AVKit (Apple opt-out of native fullscreen)", () => {
    const video = document.createElement("video");
    video.setAttribute("playsinline", "");
    video.setAttribute("webkit-playsinline", "");
    video.playsInline = true;
    allowInlinePlayback(video, false);
    expect(video.hasAttribute("playsinline")).toBe(false);
    expect(video.hasAttribute("webkit-playsinline")).toBe(false);
    expect(video.playsInline).toBe(false);
  });

  it("returns InvalidStateError text when webkitEnterFullscreen throws", () => {
    vi.stubGlobal("navigator", {
      userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X)",
      platform: "iPhone",
      maxTouchPoints: 5,
    });
    const play = vi.fn().mockResolvedValue(undefined);
    const video = {
      paused: true,
      play,
      playsInline: true,
      setAttribute: vi.fn(),
      removeAttribute: vi.fn(),
      webkitEnterFullscreen: () => {
        throw new DOMException("The object is in an invalid state.", "InvalidStateError");
      },
    } as unknown as HTMLVideoElement;
    expect(enterAvkitDetailed(video)).toEqual({
      ok: true,
      threw: "InvalidStateError: The object is in an invalid state.",
    });
    expect(play).toHaveBeenCalledOnce();
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});
