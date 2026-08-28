import type { ClientProfile } from "@/types/api.gen";

function canProbably(el: HTMLVideoElement, type: string): boolean {
  return /^probably$/i.test(el.canPlayType(type));
}

/** Conservative codec flags: only "probably" counts. ASS JS wasm is opt-in and skipped. */
export function detectClientProfile(): ClientProfile {
  const el = document.createElement("video");
  const mse = typeof MediaSource !== "undefined" || "ManagedMediaSource" in window;
  const hlsNative = Boolean(el.canPlayType("application/vnd.apple.mpegURL"));

  return {
    user_agent: navigator.userAgent,
    mse,
    hls_native: hlsNative,
    ass_js: false,
    hdr: false,
    viewport_w: Math.round(window.innerWidth),
    viewport_h: Math.round(window.innerHeight),
    hevc: canProbably(el, 'video/mp4; codecs="hvc1.1.6.L93.B0"'),
    av1: canProbably(el, 'video/mp4; codecs="av01.0.05M.08"'),
    ac3: canProbably(el, 'audio/mp4; codecs="ac-3"'),
    eac3: false,
    truehd: false,
    decoding_info: {},
  };
}

export function nativeHlsSupported(): boolean {
  const el = document.createElement("video");
  return Boolean(el.canPlayType("application/vnd.apple.mpegURL"));
}

export function sessionUrl(urls: Record<string, string>, ...keys: string[]): string | undefined {
  for (const key of keys) {
    if (urls[key]) return urls[key];
  }
  const values = Object.values(urls);
  return values[0];
}
