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

export function enterNativeFullscreen(video: HTMLVideoElement): boolean {
  const apple = video as HTMLVideoElement & { webkitEnterFullscreen?: () => void };
  if (typeof apple.webkitEnterFullscreen === "function") {
    try {
      apple.webkitEnterFullscreen();
      return true;
    } catch {
      return false;
    }
  }
  return false;
}
