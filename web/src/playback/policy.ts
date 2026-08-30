import { isIOSDevice } from "@/lib/device";
import type { PlaybackSession } from "@/types/api.gen";

export type PlaybackEngine = "direct" | "hlsjs" | "native-hls";
export type DiagnosticOwner = "hls-mms" | "hls-mse" | "native-hls" | "direct-file";
export type FullscreenStrategy = "avkit" | "element";

export type EngineCaps = {
  hlsJsSupported: boolean;
  nativeHls: boolean;
};

/** One owner. hls.js first wherever MSE/MMS exists — including iOS 17.1+. */
export function selectEngine(delivery: PlaybackSession["delivery"] | undefined, caps: EngineCaps): PlaybackEngine {
  if (delivery === "direct") return "direct";
  if (caps.hlsJsSupported) return "hlsjs";
  if (caps.nativeHls) return "native-hls";
  throw new Error("HLS is not supported in this browser");
}

export function fullscreenStrategy(): FullscreenStrategy {
  return isIOSDevice() ? "avkit" : "element";
}

/**
 * REAL DEVICE VALIDATED:
 * iPhone + hls.js + ManagedMediaSource
 * → webkitEnterFullscreen → AVKit → native seek → webkitendfullscreen
 * → same blob currentSrc → same backend session → native seek position preserved.
 *
 * A prior currentTime=0 observation was not reproduced and is not an
 * architectural limitation. Do not restore currentTime on fullscreen exit.
 */
export const IOS_AVKIT_MMS_VALIDATED = {
  engine: "hls-mms",
  enter: "webkitEnterFullscreen",
  seek: "avkit_native",
  exit: "webkitendfullscreen",
  same_blob: true,
  same_session: true,
  seek_position_preserved: true,
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
  return Number.isFinite(fallbackMs) && fallbackMs > 0 ? fallbackMs : 0;
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

/** Temporary helper for the Play Debug button. */
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
