/** iPhone / iPad (including iPadOS desktop UA). */
export function isIOSDevice(
  ua = typeof navigator !== "undefined" ? navigator.userAgent : "",
  platform = typeof navigator !== "undefined" ? navigator.platform : "",
  maxTouchPoints = typeof navigator !== "undefined" ? navigator.maxTouchPoints : 0,
): boolean {
  if (/iPad|iPhone|iPod/.test(ua)) return true;
  return platform === "MacIntel" && maxTouchPoints > 1;
}

/** Safari on macOS/iOS, or any iOS browser (all use WebKit). */
export function isAppleWebKitPlayer(
  ua = typeof navigator !== "undefined" ? navigator.userAgent : "",
  platform = typeof navigator !== "undefined" ? navigator.platform : "",
  maxTouchPoints = typeof navigator !== "undefined" ? navigator.maxTouchPoints : 0,
): boolean {
  if (isIOSDevice(ua, platform, maxTouchPoints)) return true;
  return /Safari/i.test(ua) && !/Chrome|Chromium|Edg|OPR|Android/i.test(ua);
}

type AppleVideo = HTMLVideoElement & {
  webkitEnterFullscreen?: () => void;
  webkitEnterFullScreen?: () => void;
  webkitExitFullscreen?: () => void;
  webkitExitFullScreen?: () => void;
  webkitDisplayingFullscreen?: boolean;
  webkitSupportsFullscreen?: boolean;
  webkitSetPresentationMode?: (mode: "fullscreen" | "inline" | "picture-in-picture") => void;
  webkitPresentationMode?: "fullscreen" | "inline" | "picture-in-picture";
};

function asApple(video: HTMLVideoElement): AppleVideo {
  return video as AppleVideo;
}

export function isNativeFullscreen(video: HTMLVideoElement): boolean {
  const v = asApple(video);
  return Boolean(v.webkitDisplayingFullscreen) || v.webkitPresentationMode === "fullscreen";
}

export function restoreMmsRemotePlaybackLock(video: HTMLVideoElement) {
  video.disableRemotePlayback = true;
  video.setAttribute("disableremoteplayback", "");
}

export function enterNativeFullscreen(video: HTMLVideoElement): boolean {
  const apple = asApple(video);
  try {
    // iPhone only presents AVKit from HTMLVideoElement.webkitEnterFullscreen.
    // Do not lift disableRemotePlayback here: MMS attach requires it, and
    // toggling it never produced webkitbeginfullscreen (it did reattach).
    if (isIOSDevice()) {
      const enter = apple.webkitEnterFullscreen ?? apple.webkitEnterFullScreen;
      if (typeof enter === "function") {
        enter.call(video);
        return true;
      }
    }
    if (typeof apple.webkitSetPresentationMode === "function") {
      apple.webkitSetPresentationMode("fullscreen");
      return true;
    }
    const enter = apple.webkitEnterFullscreen ?? apple.webkitEnterFullScreen;
    if (typeof enter === "function") {
      enter.call(video);
      return true;
    }
  } catch {
    return false;
  }
  return false;
}

/** Play if needed, then AVKit. Safari rejects webkitEnterFullscreen while paused. */
export function enterAvkitFromUserGesture(video: HTMLVideoElement): boolean {
  if (video.paused) {
    void video.play().catch(() => undefined);
  }
  return enterNativeFullscreen(video);
}

export function exitNativeFullscreen(video: HTMLVideoElement): boolean {
  const apple = asApple(video);
  try {
    if (typeof apple.webkitSetPresentationMode === "function" && apple.webkitPresentationMode === "fullscreen") {
      apple.webkitSetPresentationMode("inline");
      return true;
    }
    const exit = apple.webkitExitFullscreen ?? apple.webkitExitFullScreen;
    if (typeof exit === "function" && apple.webkitDisplayingFullscreen) {
      exit.call(video);
      return true;
    }
  } catch {
    return false;
  }
  return false;
}
