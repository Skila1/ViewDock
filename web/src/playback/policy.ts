import { isIOSDevice } from "@/lib/device";
import type { PlaybackSession } from "@/types/api.gen";

export type PlaybackEngine = "direct" | "hlsjs" | "native-hls";
export type DiagnosticOwner = "hls-mms" | "hls-mse" | "native-hls" | "direct-file";
export type FullscreenStrategy = "avkit" | "element";

export type EngineCaps = {
  hlsJsSupported: boolean;
  nativeHls: boolean;
};

/** One owner. Honor the server's hls_attach when present; otherwise iOS native / else hls.js. */
export function selectEngine(
  delivery: PlaybackSession["delivery"] | undefined,
  caps: EngineCaps,
  ios = isIOSDevice(),
  hlsAttach?: PlaybackSession["hls_attach"] | string,
): PlaybackEngine {
  if (delivery === "direct") return "direct";
  if (hlsAttach === "native") return "native-hls";
  if (hlsAttach === "mse") {
    if (caps.hlsJsSupported) return "hlsjs";
    if (caps.nativeHls) return "native-hls";
    throw new Error("HLS is not supported in this browser");
  }
  if (ios && caps.nativeHls) return "native-hls";
  if (caps.hlsJsSupported) return "hlsjs";
  if (caps.nativeHls) return "native-hls";
  throw new Error("HLS is not supported in this browser");
}

export function fullscreenStrategy(): FullscreenStrategy {
  return isIOSDevice() ? "avkit" : "element";
}

/**
 * iPhone + native HLS (video.src = m3u8) → webkitEnterFullscreen → AVKit.
 * MMS/hls.js never presented webkitbeginfullscreen; do not use page-fs as a stand-in.
 * Do not restore currentTime on fullscreen exit.
 */
export const IOS_AVKIT_NATIVE_HLS = {
  engine: "native-hls",
  enter: "webkitEnterFullscreen",
  seek: "avkit_native",
  exit: "webkitendfullscreen",
  same_session: true,
  restore_currentTime_on_exit: false,
} as const;

/**
 * Duration clocks are expected to differ while generation/append is in progress:
 * - movie_duration_ms: ffprobe movie length (session.duration_ms)
 * - playlist listed duration: sum of generated HLS EXTINF segments
 * - video.duration / seekable: media currently exposed/appended through MMS
 *
 * Custom chrome must never use EVENT/window video.duration as the movie length.
 */
export function movieDurationMs(session: PlaybackSession | null | undefined, fallbackMs = 0): number {
  const probed = session?.duration_ms ?? 0;
  if (probed > 0) return probed;
  if (!Number.isFinite(fallbackMs) || fallbackMs <= 0) return 0;
  return fallbackMs;
}

export function debugPlaybackEnabled(): boolean {
  if (typeof window === "undefined") return false;
  try {
    if (new URLSearchParams(window.location.search).get("vd_debug") === "1") return true;
    return window.localStorage.getItem("vd_debug") === "1";
  } catch {
    return false;
  }
}

/** Append vd_debug=1 so the diagnostics overlay can be opened by URL. */
export function withVdDebug(to: string): string {
  if (/[?&]vd_debug=/.test(to)) return to;
  return to.includes("?") ? `${to}&vd_debug=1` : `${to}?vd_debug=1`;
}

export function inferDiagnosticOwner(
  video: HTMLVideoElement | null,
  session: PlaybackSession | null | undefined,
  engine: PlaybackEngine | null,
): DiagnosticOwner | null {
  if (engine === "direct" || session?.delivery === "direct") return "direct-file";
  if (engine === "hlsjs" || session?.hls_attach === "mse") return mseOwner();
  if (engine === "native-hls" || session?.hls_attach === "native") return "native-hls";
  const src = video?.currentSrc ?? "";
  if (src.startsWith("blob:")) return mseOwner();
  if (src.includes(".m3u8")) return "native-hls";
  return null;
}

function mseOwner(): DiagnosticOwner {
  const g = globalThis as { ManagedMediaSource?: unknown; MediaSource?: unknown };
  if (typeof g.ManagedMediaSource !== "undefined") return "hls-mms";
  return "hls-mse";
}
