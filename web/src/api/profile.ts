import { isAppleWebKitPlayer, isIOSDevice } from "@/lib/device";
import type { ClientProfile, CodecCap, DecodingInfo } from "@/types/api.gen";

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

/** iOS 17.1+ ManagedMediaSource or classic MSE — use hls.js instead of AVPlayer. */
export function mseHlsAvailable(): boolean {
  return typeof MediaSource !== "undefined" || typeof (globalThis as { ManagedMediaSource?: unknown }).ManagedMediaSource !== "undefined";
}

/** iPhone/iPad use AVPlayer (m3u8 src) so fullscreen can present AVKit. */
export function usingNativeHls(): boolean {
  if (isIOSDevice() && nativeHlsSupported()) return true;
  return nativeHlsSupported() && !mseHlsAvailable();
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

function mseTypeSupported(type: string): boolean {
  const g = globalThis as {
    ManagedMediaSource?: { isTypeSupported?: (t: string) => boolean };
    MediaSource?: { isTypeSupported?: (t: string) => boolean };
  };
  const ms = g.ManagedMediaSource ?? g.MediaSource;
  return Boolean(ms?.isTypeSupported?.(type));
}

type Probe = { supported: boolean; powerEfficient: boolean };

// Level 5.1 strings, so a 4K stream is what the decoder is asked about.
const CODEC_TYPES: Record<string, { sdr: string; tenBit?: string }> = {
  h264: { sdr: 'video/mp4; codecs="avc1.640033"' },
  hevc: { sdr: 'video/mp4; codecs="hvc1.1.6.L153.B0"', tenBit: 'video/mp4; codecs="hvc1.2.4.L153.B0"' },
  av1: { sdr: 'video/mp4; codecs="av01.0.13M.08"', tenBit: 'video/mp4; codecs="av01.0.13M.10"' },
};

async function probeVideo(contentType: string, type: "file" | "media-source", height: number): Promise<Probe | undefined> {
  const mc = navigator.mediaCapabilities;
  if (!mc?.decodingInfo) return undefined;
  try {
    const info = await mc.decodingInfo({
      type,
      video: {
        contentType,
        width: Math.round((height * 16) / 9),
        height,
        bitrate: height > 1080 ? 60_000_000 : 15_000_000,
        framerate: 24,
      },
    });
    return { supported: Boolean(info.supported), powerEfficient: Boolean(info.powerEfficient) };
  } catch {
    return undefined;
  }
}

/**
 * codecCap measures one codec. 4K counts only with a hardware (power
 * efficient) decoder: the GPU decodes it, where software decoding would
 * stutter. Without MediaCapabilities, MSE support means 1080p.
 */
async function codecCap(codec: string, type: "file" | "media-source"): Promise<CodecCap | undefined> {
  const types = CODEC_TYPES[codec];
  const [uhd, fhd] = await Promise.all([probeVideo(types.sdr, type, 2160), probeVideo(types.sdr, type, 1080)]);
  let max = 0;
  let hardware = false;
  if (uhd?.supported && uhd.powerEfficient) {
    max = 2160;
    hardware = true;
  } else if (fhd?.supported) {
    max = 1080;
    hardware = fhd.powerEfficient;
  } else if (!uhd && !fhd && (type === "media-source" ? mseTypeSupported(types.sdr) : canProbably(document.createElement("video"), types.sdr))) {
    max = 1080;
  }
  if (max === 0) return undefined;
  let tenBit = false;
  if (types.tenBit) {
    const deep = await probeVideo(types.tenBit, type, max);
    tenBit = deep ? deep.supported && (max < 2160 || deep.powerEfficient) : type === "media-source" && mseTypeSupported(types.tenBit);
  }
  return { max_height: max, hardware, ten_bit: tenBit };
}

const FALLBACK_KEY = "viewdock:codec-fallback";

/**
 * Called when this browser failed to play a stream it claimed to decode.
 * For the rest of the tab, external sources send H.264 only.
 */
export function rememberCodecFallback() {
  try {
    sessionStorage.setItem(FALLBACK_KEY, "1");
  } catch {
    /* storage blocked: the next session may try the codec again */
  }
}

export function codecFallbackActive(): boolean {
  try {
    return sessionStorage.getItem(FALLBACK_KEY) === "1";
  } catch {
    return false;
  }
}

let measured: Promise<Record<string, CodecCap>> | null = null;

/** Video decoders do not change while the page is open, so they are measured once. */
export function measureCodecs(type: "file" | "media-source"): Promise<Record<string, CodecCap>> {
  if (!measured) {
    measured = (async () => {
      const out: Record<string, CodecCap> = {};
      const caps = await Promise.all(Object.keys(CODEC_TYPES).map(async (c) => [c, await codecCap(c, type)] as const));
      for (const [codec, cap] of caps) if (cap) out[codec] = cap;
      // Every browser ViewDock supports plays 1080p H.264.
      if (!out.h264) out.h264 = { max_height: 1080, hardware: false, ten_bit: false };
      return out;
    })().catch(() => {
      measured = null;
      return { h264: { max_height: 1080, hardware: false, ten_bit: false } };
    });
  }
  return measured;
}

/** Conservative codec flags. MediaCapabilities wins when present; otherwise "probably". */
export async function detectClientProfile(): Promise<ClientProfile> {
  const el = document.createElement("video");
  const apple = isAppleWebKitPlayer();
  const mse = mseHlsAvailable();
  const nativeHls = usingNativeHls();
  const decoding_info: DecodingInfo = {};
  // Native AVPlayer can decode HEVC/EAC3 that ManagedMediaSource cannot append.
  const probeType = nativeHls ? "file" : "media-source";

  const [hevc, hevc10, av1, ac3, eac3, measuredCodecs] = await Promise.all([
    decodingInfo("video", HEVC_MAIN, probeType),
    decodingInfo("video", HEVC_MAIN10, probeType),
    decodingInfo("video", AV1, probeType),
    decodingInfo("audio", AC3, probeType),
    decodingInfo("audio", EAC3, probeType),
    measureCodecs(probeType),
  ]);
  const fallback = codecFallbackActive();
  const codecs = fallback ? { h264: measuredCodecs.h264 } : measuredCodecs;
  record(decoding_info, "hevc", hevc);
  record(decoding_info, "hevc_main10", hevc10);
  record(decoding_info, "av1", av1);
  record(decoding_info, "ac3", ac3);
  record(decoding_info, "eac3", eac3);

  const hevcMain10 = nativeHls
    ? (hevc10?.supported ?? (canPlayLoose(el, HEVC_MAIN10) || true))
    : (hevc10?.supported ?? mseTypeSupported(HEVC_MAIN10));
  const hevcMain = nativeHls
    ? (hevc?.supported ?? (canPlayLoose(el, HEVC_MAIN) || true))
    : (hevc?.supported ?? mseTypeSupported(HEVC_MAIN));

  return {
    user_agent: navigator.userAgent,
    mse,
    hls_native: nativeHls,
    ass_js: false,
    hdr: false,
    viewport_w: Math.round(window.innerWidth),
    viewport_h: Math.round(window.innerHeight),
    hevc: hevcMain && !fallback,
    hevc_main10: hevcMain10 && !fallback,
    av1: apple || fallback ? false : (av1?.supported ?? canProbably(el, AV1)),
    ac3: nativeHls
      ? (ac3?.supported ?? (canPlayLoose(el, AC3) || true))
      : (ac3?.supported ?? mseTypeSupported(AC3)),
    eac3: nativeHls
      ? (eac3?.supported ?? (canPlayLoose(el, EAC3) || true))
      : (eac3?.supported ?? mseTypeSupported(EAC3)),
    truehd: false,
    decoding_info,
    codecs,
  };
}

export function sessionUrl(urls: Record<string, string>, ...keys: string[]): string | undefined {
  for (const key of keys) {
    if (urls[key]) return urls[key];
  }
  const values = Object.values(urls);
  return values[0];
}
