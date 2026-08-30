import { isIOSDevice } from "@/lib/device";
import type { PlaybackSession } from "@/types/api.gen";

export type PlaybackEngine = "direct" | "hlsjs" | "native-hls";
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

/** Custom chrome must never use EVENT/window video.duration as the movie length. */
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
