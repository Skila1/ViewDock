import { describe, expect, it, vi, afterEach } from "vitest";
import { enterAvkitFromUserGesture, enterNativeFullscreen, isAppleWebKitPlayer, isIOSDevice } from "./device";

describe("device", () => {
  it("detects iPhone and iPad", () => {
    expect(isIOSDevice("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)")).toBe(true);
    expect(isIOSDevice("Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)")).toBe(true);
    expect(isIOSDevice("Mozilla/5.0 (Windows NT 10.0; Win64; x64)")).toBe(false);
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
    const video = {
      webkitEnterFullscreen: enter,
      webkitSetPresentationMode: present,
      webkitSupportsFullscreen: true,
    } as unknown as HTMLVideoElement;
    expect(enterNativeFullscreen(video)).toBe(true);
    expect(enter).toHaveBeenCalledOnce();
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
      webkitEnterFullscreen: enter,
    } as unknown as HTMLVideoElement;
    expect(enterAvkitFromUserGesture(video)).toBe(true);
    expect(play).toHaveBeenCalledOnce();
    expect(enter).toHaveBeenCalledOnce();
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});
