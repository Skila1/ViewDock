import { isAppleWebKitPlayer } from "@/lib/device";
import type { ClientProfile, DecodingInfo } from "@/types/api.gen";

const HEVC_MAIN = 'video/mp4; codecs="hvc1.1.6.L93.B0"';
const HEVC_MAIN10 = 'video/mp4; codecs="hvc1.2.4.L120.B0"';
const AV1 = 'video/mp4; codecs="av01.0.05M.08"';
const AC3 = 'audio/mp4; codecs="ac-3"';
const EAC3 = 'audio/mp4; codecs="ec-3"';

function canProbably(el: HTMLVideoElement, type: string): boolean {
  return /^probably$/i.test(el.canPlayType(type));
}

function canPlayLoose(el: HTMLVideoElement, type: string): boolean {
  return /probably|maybe/i.test(el.canPlayType(type));
}

/** Safari / iOS only. Chromium often claims HLS it cannot play natively. */
export function nativeHlsSupported(): boolean {
  if (!isAppleWebKitPlayer()) return false;
  const el = document.createElement("video");
  return Boolean(el.canPlayType("application/vnd.apple.mpegURL"));
}

type Decoded = { supported: boolean; smooth?: boolean };

async function decodingInfo(
  kind: "video" | "audio",
  contentType: string,
  type: "file" | "media-source",
): Promise<Decoded | undefined> {
  const mc = navigator.mediaCapabilities;
  if (!mc?.decodingInfo) return undefined;
  try {
    const cfg = kind === "video"
      ? {
          type,
          video: { contentType, width: 1920, height: 1080, bitrate: 8_000_000, framerate: 24 },
        }
      : {
          type,
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
  const apple = isAppleWebKitPlayer();
  const mse = typeof MediaSource !== "undefined" || "ManagedMediaSource" in window;
  const decoding_info: DecodingInfo = {};
  const probeType = apple ? "file" : "media-source";

  const [hevc, hevc10, av1, ac3, eac3] = await Promise.all([
    decodingInfo("video", HEVC_MAIN, probeType),
    decodingInfo("video", HEVC_MAIN10, probeType),
    decodingInfo("video", AV1, probeType),
    decodingInfo("audio", AC3, probeType),
    decodingInfo("audio", EAC3, probeType),
  ]);
  record(decoding_info, "hevc", hevc);
  record(decoding_info, "hevc_main10", hevc10);
  record(decoding_info, "av1", av1);
  record(decoding_info, "ac3", ac3);
  record(decoding_info, "eac3", eac3);

  const hevcMain10 = apple
    ? (hevc10?.supported ?? (canPlayLoose(el, HEVC_MAIN10) || true))
    : (hevc10?.supported ?? false);
  const hevcMain = apple
    ? (hevc?.supported ?? (canPlayLoose(el, HEVC_MAIN) || true))
    : (hevc?.supported ?? canProbably(el, HEVC_MAIN));

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
    av1: apple ? false : (av1?.supported ?? canProbably(el, AV1)),
    ac3: apple ? (ac3?.supported ?? (canPlayLoose(el, AC3) || true)) : (ac3?.supported ?? canProbably(el, AC3)),
    eac3: apple ? (eac3?.supported ?? (canPlayLoose(el, EAC3) || true)) : (eac3?.supported ?? canProbably(el, EAC3)),
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
