/** iPhone / iPod only. iPad plays inline without requiring AVKit. */
export function isIPhone(
  ua = typeof navigator !== "undefined" ? navigator.userAgent : "",
): boolean {
  return /iPhone|iPod/.test(ua);
}

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

/** playsinline is Apple's opt-out of AVKit. Strip it before webkitEnterFullscreen. */
export function allowInlinePlayback(video: HTMLVideoElement, on: boolean) {
  video.playsInline = on;
  if (on) {
    video.setAttribute("playsinline", "");
    video.setAttribute("webkit-playsinline", "");
  } else {
    video.removeAttribute("playsinline");
    video.removeAttribute("webkit-playsinline");
  }
}

export type AvkitEnterResult = { ok: boolean; threw?: string };

/**
 * On iPhone, playsinline is Apple's opt-out of AVKit. Live logs showed
 * webkitEnterFullscreen accepted (supports=true, no throw) and never
 * presented. Apple's documented path is play() without playsinline.
 * https://webkit.org/blog/6784/new-video-policies-for-ios/
 */
export function enterAvkitDetailed(video: HTMLVideoElement): AvkitEnterResult {
  const apple = asApple(video);
  try {
    if (isIPhone()) {
      allowInlinePlayback(video, false);
      const enter = apple.webkitEnterFullscreen ?? apple.webkitEnterFullScreen;
      if (typeof enter === "function") {
        try {
          enter.call(video);
        } catch (err) {
          const threw = err instanceof Error ? `${err.name}: ${err.message}` : String(err);
          if (video.paused) void video.play().catch(() => undefined);
          else {
            video.pause();
            void video.play().catch(() => undefined);
          }
          return { ok: true, threw };
        }
      }
      if (video.paused) {
        void video.play().catch(() => undefined);
      } else {
        video.pause();
        void video.play().catch(() => undefined);
      }
      return { ok: true };
    }
    if (typeof apple.webkitSetPresentationMode === "function") {
      apple.webkitSetPresentationMode("fullscreen");
      return { ok: true };
    }
    const enter = apple.webkitEnterFullscreen ?? apple.webkitEnterFullScreen;
    if (typeof enter === "function") {
      enter.call(video);
      return { ok: true };
    }
  } catch (err) {
    const threw = err instanceof Error ? `${err.name}: ${err.message}` : String(err);
    return { ok: false, threw };
  }
  return { ok: false };
}

export function enterNativeFullscreen(video: HTMLVideoElement): boolean {
  return enterAvkitDetailed(video).ok;
}

export function enterAvkitFromUserGesture(video: HTMLVideoElement): boolean {
  return enterAvkitDetailed(video).ok;
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
