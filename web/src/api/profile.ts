import type { ClientProfile, DecodingInfo } from "@/types/api.gen";

const HEVC_MAIN = 'video/mp4; codecs="hvc1.1.6.L93.B0"';
const HEVC_MAIN10 = 'video/mp4; codecs="hvc1.2.4.L120.B0"';
const AV1 = 'video/mp4; codecs="av01.0.05M.08"';
const AC3 = 'audio/mp4; codecs="ac-3"';
const EAC3 = 'audio/mp4; codecs="ec-3"';

function canProbably(el: HTMLVideoElement, type: string): boolean {
  return /^probably$/i.test(el.canPlayType(type));
}

/** Safari / iOS only. Chromium often claims HLS it cannot play natively. */
export function nativeHlsSupported(): boolean {
  const ua = navigator.userAgent;
  const iOS = /iPad|iPhone|iPod/.test(ua) ||
    (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  const safari = /Safari/i.test(ua) && !/Chrome|Chromium|Edg|OPR|Android/i.test(ua);
  if (!iOS && !safari) return false;
  const el = document.createElement("video");
  return Boolean(el.canPlayType("application/vnd.apple.mpegURL"));
}

type Decoded = { supported: boolean; smooth?: boolean };

async function decodingInfo(kind: "video" | "audio", contentType: string): Promise<Decoded | undefined> {
  const mc = navigator.mediaCapabilities;
  if (!mc?.decodingInfo) return undefined;
  try {
    const cfg = kind === "video"
      ? {
          type: "media-source" as const,
          video: { contentType, width: 1920, height: 1080, bitrate: 8_000_000, framerate: 24 },
        }
      : {
          type: "media-source" as const,
          audio: { contentType, channels: "6", bitrate: 640_000 },
        };
    const info = await mc.decodingInfo(cfg);
    return { supported: Boolean(info.supported), smooth: info.smooth };
  } catch {
    return undefined;
  }
}

function record(info: DecodingInfo, key: string, value: Decoded | undefined) {
  if (!value) return;
  info[key] = value;
}

/** Conservative codec flags. MediaCapabilities wins when present; otherwise "probably". */
export async function detectClientProfile(): Promise<ClientProfile> {
  const el = document.createElement("video");
  const mse = typeof MediaSource !== "undefined" || "ManagedMediaSource" in window;
  const decoding_info: DecodingInfo = {};

  const [hevc, hevc10, av1, ac3, eac3] = await Promise.all([
    decodingInfo("video", HEVC_MAIN),
    decodingInfo("video", HEVC_MAIN10),
    decodingInfo("video", AV1),
    decodingInfo("audio", AC3),
    decodingInfo("audio", EAC3),
  ]);
  record(decoding_info, "hevc", hevc);
  record(decoding_info, "hevc_main10", hevc10);
  record(decoding_info, "av1", av1);
  record(decoding_info, "ac3", ac3);
  record(decoding_info, "eac3", eac3);

  const hevcMain10 = hevc10?.supported ?? false;
  const hevcMain = hevc?.supported ?? canProbably(el, HEVC_MAIN);

  return {
    user_agent: navigator.userAgent,
    mse,
    hls_native: nativeHlsSupported(),
    ass_js: false,
    hdr: false,
    viewport_w: Math.round(window.innerWidth),
    viewport_h: Math.round(window.innerHeight),
    hevc: hevcMain,
    hevc_main10: hevcMain10,
    av1: av1?.supported ?? canProbably(el, AV1),
    ac3: ac3?.supported ?? canProbably(el, AC3),
    eac3: eac3?.supported ?? canProbably(el, EAC3),
    truehd: false,
    decoding_info,
  };
}

export function sessionUrl(urls: Record<string, string>, ...keys: string[]): string | undefined {
  for (const key of keys) {
    if (urls[key]) return urls[key];
  }
  const values = Object.values(urls);
  return values[0];
}
