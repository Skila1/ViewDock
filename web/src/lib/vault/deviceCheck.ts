import { mseHlsAvailable, nativeHlsSupported, usingNativeHls } from "@/api/profile";
import { SPEED_TEST_PATH } from "@/api/vault";
import { chunkAad, decryptChunk, encryptChunk, generateVaultKey } from "./crypto";
import { listRecords } from "./db";
import { serviceWorkerServesVault, storageEstimate, storagePersisted, vaultMediaUrl } from "./manager";

// Evidence distinguishes what the browser claims from what was exercised.
export type Evidence = "reported" | "verified" | "measured";
export type CheckStatus = "pass" | "warn" | "fail" | "info";
export type CheckGroup = "video" | "audio" | "streaming" | "display" | "playback" | "storage" | "offline" | "network";

export type DeviceCheck = {
  id: string;
  group: CheckGroup;
  label: string;
  status: CheckStatus;
  evidence: Evidence;
  detail: string;
  guidance?: string;
};

export type DeviceReport = { ranAt: string; userAgent: string; checks: DeviceCheck[] };

export const GROUP_LABELS: Record<CheckGroup, string> = {
  video: "Video codecs",
  audio: "Audio formats",
  streaming: "Streaming",
  display: "Display",
  playback: "Playback tests",
  storage: "Storage",
  offline: "Offline readiness",
  network: "Network to this server",
};

const REPORT_KEY = "viewdock:device-test:v1";

export function median(values: readonly number[]): number {
  if (!values.length) return NaN;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2;
}

export function mbps(bytes: number, ms: number): number {
  if (!(ms > 0) || !(bytes > 0)) return 0;
  return (bytes * 8) / (ms / 1000) / 1_000_000;
}

export function throughputAdvice(value: number): { status: CheckStatus; guidance: string } {
  if (value < 3) return { status: "fail", guidance: "Under 3 Mbps: choose 480p, or save titles to the Offline Vault on a faster connection." };
  if (value < 8) return { status: "warn", guidance: "Enough for 720p. 1080p may buffer; pick 720p in the player if it stalls." };
  if (value < 25) return { status: "pass", guidance: "Enough for 1080p streaming." };
  return { status: "pass", guidance: "Enough for high-bitrate direct play, including most 4K files." };
}

export function latencyAdvice(ms: number): { status: CheckStatus; guidance?: string } {
  if (ms > 400) return { status: "fail", guidance: "High latency makes seeking slow and watch party sync looser. Prefer a closer server or wired connection." };
  if (ms > 150) return { status: "warn", guidance: "Seeking and starting playback may feel slow on this connection." };
  return { status: "pass" };
}

export type CodecProbe = { file: string; mse?: boolean; decoding?: { supported: boolean; smooth?: boolean } };

/** codecStatus favours MediaCapabilities, then MSE, then canPlayType "probably". */
export function codecStatus(probe: CodecProbe): { supported: boolean; detail: string } {
  const parts = [`Direct file: ${probe.file || "no"}`];
  if (probe.mse !== undefined) parts.push(`Media Source: ${probe.mse ? "yes" : "no"}`);
  if (probe.decoding) parts.push(`Decoder: ${probe.decoding.supported ? (probe.decoding.smooth ? "smooth" : "supported, may not be smooth") : "unsupported"}`);
  const supported = probe.decoding?.supported ?? (probe.mse === true || probe.file === "probably");
  return { supported, detail: parts.join(" · ") };
}

export function summarize(checks: readonly DeviceCheck[]): Record<CheckStatus, number> {
  const out: Record<CheckStatus, number> = { pass: 0, warn: 0, fail: 0, info: 0 };
  for (const check of checks) out[check.status]++;
  return out;
}

export function loadReport(): DeviceReport | null {
  try {
    const raw = localStorage.getItem(REPORT_KEY);
    return raw ? (JSON.parse(raw) as DeviceReport) : null;
  } catch {
    return null;
  }
}

export function saveReport(report: DeviceReport) {
  try {
    localStorage.setItem(REPORT_KEY, JSON.stringify(report));
  } catch {
    // Results remain on screen when storage is unavailable.
  }
}

type MseCtor = { isTypeSupported?: (type: string) => boolean };

function mseSupports(type: string): boolean | undefined {
  const g = globalThis as { ManagedMediaSource?: MseCtor; MediaSource?: MseCtor };
  const ms = g.ManagedMediaSource ?? g.MediaSource;
  if (!ms?.isTypeSupported) return undefined;
  try {
    return ms.isTypeSupported(type);
  } catch {
    return false;
  }
}

async function decoding(kind: "video" | "audio", contentType: string): Promise<{ supported: boolean; smooth?: boolean } | undefined> {
  const mc = navigator.mediaCapabilities;
  if (!mc?.decodingInfo) return undefined;
  try {
    const info = await mc.decodingInfo(
      kind === "video"
        ? { type: "file", video: { contentType, width: 1920, height: 1080, bitrate: 8_000_000, framerate: 24 } }
        : { type: "file", audio: { contentType, channels: "2", bitrate: 192_000, samplerate: 48_000 } },
    );
    return { supported: info.supported, smooth: info.smooth };
  } catch {
    return undefined;
  }
}

const VIDEO_CODECS: { id: string; label: string; type: string; required?: boolean; fallback: string }[] = [
  { id: "h264", label: "H.264 (AVC)", type: 'video/mp4; codecs="avc1.640028"', required: true, fallback: "ViewDock cannot play video in this browser. Try a current Chrome, Edge, Firefox or Safari." },
  { id: "hevc", label: "HEVC (H.265)", type: 'video/mp4; codecs="hvc1.1.6.L93.B0"', fallback: "HEVC titles are transcoded to H.264 by the server, which uses more server resources." },
  { id: "hevc10", label: "HEVC Main 10", type: 'video/mp4; codecs="hvc1.2.4.L120.B0"', fallback: "10-bit HEVC titles are transcoded to H.264." },
  { id: "av1", label: "AV1", type: 'video/mp4; codecs="av01.0.08M.08"', fallback: "AV1 titles are transcoded to H.264." },
  { id: "vp9", label: "VP9", type: 'video/webm; codecs="vp09.00.40.08"', fallback: "VP9 titles are transcoded to H.264." },
];

const AUDIO_CODECS: { id: string; label: string; type: string; required?: boolean; fallback: string }[] = [
  { id: "aac", label: "AAC", type: 'audio/mp4; codecs="mp4a.40.2"', required: true, fallback: "AAC is needed for most titles; audio will not play in this browser." },
  { id: "opus", label: "Opus", type: 'audio/webm; codecs="opus"', fallback: "Opus audio tracks are converted to AAC by the server." },
  { id: "mp3", label: "MP3", type: "audio/mpeg", fallback: "MP3 audio tracks are converted to AAC." },
  { id: "flac", label: "FLAC", type: "audio/flac", fallback: "Lossless FLAC audio is converted to AAC." },
  { id: "ac3", label: "Dolby Digital (AC-3)", type: 'audio/mp4; codecs="ac-3"', fallback: "Surround AC-3 audio is converted to stereo or 5.1 AAC." },
  { id: "eac3", label: "Dolby Digital Plus (E-AC-3)", type: 'audio/mp4; codecs="ec-3"', fallback: "E-AC-3 audio is converted to AAC." },
];

async function codecChecks(el: HTMLVideoElement): Promise<DeviceCheck[]> {
  const out: DeviceCheck[] = [];
  for (const [group, list, kind] of [["video", VIDEO_CODECS, "video"], ["audio", AUDIO_CODECS, "audio"]] as const) {
    for (const codec of list) {
      const probe: CodecProbe = { file: el.canPlayType(codec.type), mse: mseSupports(codec.type), decoding: await decoding(kind, codec.type) };
      const { supported, detail } = codecStatus(probe);
      out.push({
        id: `${group}.${codec.id}`,
        group,
        label: codec.label,
        status: supported ? "pass" : codec.required ? "fail" : "warn",
        evidence: "reported",
        detail,
        guidance: supported ? undefined : codec.fallback,
      });
    }
  }
  return out;
}

function streamingChecks(): DeviceCheck[] {
  const native = nativeHlsSupported();
  const mse = mseHlsAvailable();
  const path = usingNativeHls();
  return [
    { id: "streaming.hls_native", group: "streaming", label: "Native HLS", status: native ? "pass" : "info", evidence: "reported", detail: native ? "The browser plays HLS itself." : "Not available; hls.js is used instead." },
    {
      id: "streaming.mse",
      group: "streaming",
      label: "Media Source Extensions",
      status: mse ? "pass" : native ? "info" : "fail",
      evidence: "reported",
      detail: mse ? "Available for adaptive streaming." : "Not available.",
      guidance: mse || native ? undefined : "Neither native HLS nor Media Source is available, so transcoded streams cannot play.",
    },
    { id: "streaming.path", group: "streaming", label: "Playback engine", status: "info", evidence: "reported", detail: path ? "Native HLS player" : "hls.js over Media Source" },
  ];
}

function displayChecks(): DeviceCheck[] {
  const dpr = window.devicePixelRatio || 1;
  const w = Math.round(window.screen.width * dpr);
  const h = Math.round(window.screen.height * dpr);
  const hdr = window.matchMedia?.("(dynamic-range: high)").matches ?? false;
  const wide = window.matchMedia?.("(color-gamut: p3)").matches ?? false;
  return [
    { id: "display.resolution", group: "display", label: "Screen resolution", status: "info", evidence: "reported", detail: `${w} × ${h} physical pixels`, guidance: h < 1080 ? "Titles above 1080p are downscaled on this screen; 1080p or lower saves bandwidth." : undefined },
    {
      id: "display.hdr",
      group: "display",
      label: "HDR display",
      status: hdr ? "info" : "warn",
      evidence: "reported",
      detail: `${hdr ? "The display reports high dynamic range" : "Standard dynamic range"}${wide ? ", wide colour gamut" : ""}.`,
      guidance: hdr ? "Reported only. HDR and Dolby Vision output are not verified by this test." : "HDR titles are shown tone-mapped or as SDR.",
    },
  ];
}

function waitFor(target: EventTarget, ok: string, fail: string, ms: number): Promise<boolean> {
  return new Promise((resolve) => {
    const timer = setTimeout(() => finish(false), ms);
    const onOk = () => finish(true);
    const onFail = () => finish(false);
    function finish(value: boolean) {
      clearTimeout(timer);
      target.removeEventListener(ok, onOk);
      target.removeEventListener(fail, onFail);
      resolve(value);
    }
    target.addEventListener(ok, onOk, { once: true });
    target.addEventListener(fail, onFail, { once: true });
  });
}

/** Records a short canvas clip and plays it back, exercising decode without DRM. */
async function drmFreePlayback(): Promise<DeviceCheck> {
  const base = { id: "playback.drm_free", group: "playback" as const, label: "DRM-free playback" };
  const canvas = document.createElement("canvas");
  canvas.width = 160;
  canvas.height = 90;
  const capture = (canvas as HTMLCanvasElement & { captureStream?: (fps?: number) => MediaStream }).captureStream;
  if (typeof MediaRecorder === "undefined" || typeof capture !== "function") {
    return { ...base, status: "info", evidence: "reported", detail: "This browser cannot record a test clip, so playback was not verified here. ViewDock does not use DRM, so no DRM module is needed." };
  }
  const type = ["video/webm;codecs=vp8", "video/webm", "video/mp4"].find((t) => MediaRecorder.isTypeSupported(t));
  if (!type) return { ...base, status: "info", evidence: "reported", detail: "No recordable test format; playback was not verified here." };
  const ctx = canvas.getContext("2d");
  const stream = capture.call(canvas, 15);
  const recorder = new MediaRecorder(stream, { mimeType: type });
  const parts: Blob[] = [];
  recorder.ondataavailable = (event) => {
    if (event.data.size) parts.push(event.data);
  };
  let frame = 0;
  const paint = setInterval(() => {
    if (!ctx) return;
    ctx.fillStyle = frame++ % 2 ? "#1d9bf0" : "#16a34a";
    ctx.fillRect(0, 0, canvas.width, canvas.height);
  }, 60);
  const stopped = waitFor(recorder, "stop", "error", 4000);
  recorder.start(100);
  await new Promise((r) => setTimeout(r, 900));
  recorder.stop();
  await stopped;
  clearInterval(paint);
  stream.getTracks().forEach((t) => t.stop());
  const url = URL.createObjectURL(new Blob(parts, { type }));
  const video = document.createElement("video");
  video.muted = true;
  video.playsInline = true;
  video.src = url;
  try {
    const loaded = await waitFor(video, "loadeddata", "error", 5000);
    if (!loaded) return { ...base, status: "fail", evidence: "verified", detail: "A locally recorded test clip failed to decode.", guidance: "Media playback appears to be blocked or broken in this browser. Check extensions and hardware acceleration settings." };
    await video.play().catch(() => undefined);
    await new Promise((r) => setTimeout(r, 300));
    const advanced = video.currentTime > 0 || !video.paused;
    return advanced
      ? { ...base, status: "pass", evidence: "verified", detail: "A test clip decoded and played without any DRM module." }
      : { ...base, status: "warn", evidence: "verified", detail: "The test clip decoded but did not advance; autoplay may be restricted.", guidance: "Tap play if titles do not start on their own." };
  } finally {
    video.removeAttribute("src");
    video.load();
    URL.revokeObjectURL(url);
  }
}

function subtitleCheck(): DeviceCheck {
  const base = { id: "playback.subtitles", group: "playback" as const, label: "Text subtitles" };
  try {
    const video = document.createElement("video");
    const track = video.addTextTrack("subtitles", "test", "en");
    track.mode = "hidden";
    track.addCue(new VTTCue(0, 1, "ViewDock"));
    if (track.cues?.length === 1) return { ...base, status: "pass", evidence: "verified", detail: "WebVTT cues can be created and attached." };
    return { ...base, status: "warn", evidence: "verified", detail: "Text tracks exist but cues did not attach.", guidance: "Subtitles may need to be burned in by the server." };
  } catch {
    return { ...base, status: "warn", evidence: "verified", detail: "The text track API is unavailable.", guidance: "Subtitles will be burned into the video by the server, which requires a transcode." };
  }
}

async function storageChecks(): Promise<DeviceCheck[]> {
  const out: DeviceCheck[] = [];
  let idb = false;
  try {
    await new Promise<void>((resolve, reject) => {
      const request = indexedDB.open("viewdock-device-test");
      request.onsuccess = () => {
        request.result.close();
        indexedDB.deleteDatabase("viewdock-device-test");
        resolve();
      };
      request.onerror = () => reject(request.error);
    });
    idb = true;
  } catch {
    idb = false;
  }
  out.push({ id: "storage.indexeddb", group: "storage", label: "IndexedDB", status: idb ? "pass" : "fail", evidence: "verified", detail: idb ? "A test database opened." : "IndexedDB is unavailable.", guidance: idb ? undefined : "Offline titles and cached pages cannot be stored. Private browsing often disables storage." });

  let cryptoOk = false;
  try {
    const key = await generateVaultKey();
    const aad = chunkAad("device-test", 0);
    const sealed = await encryptChunk(key, new TextEncoder().encode("viewdock"), aad);
    cryptoOk = new TextDecoder().decode(await decryptChunk(key, sealed, aad)) === "viewdock";
  } catch {
    cryptoOk = false;
  }
  out.push({ id: "storage.crypto", group: "storage", label: "Encryption (WebCrypto)", status: cryptoOk ? "pass" : "fail", evidence: "verified", detail: cryptoOk ? "AES-GCM encrypt and decrypt round trip succeeded." : "WebCrypto AES-GCM is unavailable.", guidance: cryptoOk ? undefined : "The Offline Vault needs WebCrypto, which requires HTTPS or localhost." });

  const estimate = await storageEstimate();
  const free = estimate.quota ? Math.max(0, estimate.quota - (estimate.usage ?? 0)) : undefined;
  out.push({
    id: "storage.quota",
    group: "storage",
    label: "Available storage",
    status: free === undefined ? "info" : free < 2 * 1024 ** 3 ? "warn" : "pass",
    evidence: "measured",
    detail: free === undefined ? "The browser does not report a quota." : `About ${(free / 1024 ** 3).toFixed(1)} GB free for this site.`,
    guidance: free !== undefined && free < 2 * 1024 ** 3 ? "Less than 2 GB is free, which fits few feature-length titles." : undefined,
  });

  const persisted = await storagePersisted();
  out.push({ id: "storage.persisted", group: "storage", label: "Persistent storage", status: persisted ? "pass" : "warn", evidence: "reported", detail: persisted ? "Granted." : "Not granted; the browser may evict offline titles.", guidance: persisted ? undefined : "Saving a title offline asks the browser to keep storage. Installing the app usually helps." });

  const opfs = typeof navigator.storage?.getDirectory === "function";
  out.push({ id: "storage.opfs", group: "storage", label: "Origin private file system", status: "info", evidence: "reported", detail: opfs ? "Available." : "Not available; IndexedDB is used." });
  return out;
}

async function offlineChecks(userId: string | undefined): Promise<DeviceCheck[]> {
  const out: DeviceCheck[] = [];
  const supported = typeof navigator !== "undefined" && "serviceWorker" in navigator;
  const controlled = Boolean(navigator.serviceWorker?.controller);
  out.push({
    id: "offline.sw",
    group: "offline",
    label: "Service worker",
    status: controlled ? "pass" : supported ? "warn" : "fail",
    evidence: "reported",
    detail: controlled ? "Active and controlling this page." : supported ? "Supported but not controlling this page yet." : "Not supported.",
    guidance: controlled ? undefined : supported ? "Reload once while online so the app can work offline." : "This browser cannot open ViewDock offline.",
  });
  let shell = false;
  try {
    shell = Boolean(await caches.match("/"));
  } catch {
    shell = false;
  }
  out.push({ id: "offline.shell", group: "offline", label: "Offline app shell", status: shell ? "pass" : "warn", evidence: "verified", detail: shell ? "The app shell is cached." : "The app shell is not cached yet.", guidance: shell ? undefined : "Open ViewDock once while online so it can start without a connection." });

  const vaultSw = controlled && (await serviceWorkerServesVault());
  out.push({ id: "offline.vault_sw", group: "offline", label: "Offline Vault playback", status: vaultSw ? "pass" : "warn", evidence: "verified", detail: vaultSw ? "The service worker serves seekable offline titles." : "The service worker cannot serve offline titles yet.", guidance: vaultSw ? undefined : "Offline titles fall back to in-memory playback, limited to smaller files." });

  if (vaultSw && userId) {
    const ready = (await listRecords(userId).catch(() => [])).find((r) => r.status === "complete" && r.manifest);
    if (ready) out.push(await vaultSeekCheck(vaultMediaUrl(ready.key), ready.title));
  }
  return out;
}

async function vaultSeekCheck(url: string, title: string): Promise<DeviceCheck> {
  const base = { id: "offline.vault_seek", group: "offline" as const, label: "Offline seek test" };
  const video = document.createElement("video");
  video.muted = true;
  video.preload = "metadata";
  video.src = url;
  try {
    if (!(await waitFor(video, "loadedmetadata", "error", 8000)) || !Number.isFinite(video.duration)) {
      return { ...base, status: "fail", evidence: "verified", detail: `“${title}” could not be opened from the vault.`, guidance: "The file's format may not be supported by this browser; see the codec results above." };
    }
    const seeked = waitFor(video, "seeked", "error", 8000);
    video.currentTime = video.duration / 2;
    return (await seeked)
      ? { ...base, status: "pass", evidence: "verified", detail: `Opened “${title}” offline and seeked to the middle.` }
      : { ...base, status: "fail", evidence: "verified", detail: `Seeking in “${title}” failed.` };
  } finally {
    video.removeAttribute("src");
    video.load();
  }
}

async function timed(url: string, headers: Record<string, string>): Promise<{ ms: number; bytes: number; status: number }> {
  const started = performance.now();
  const res = await fetch(url, { headers, credentials: "include", cache: "no-store" });
  const bytes = (await res.arrayBuffer()).byteLength;
  return { ms: performance.now() - started, bytes, status: res.status };
}

async function networkChecks(): Promise<DeviceCheck[]> {
  const out: DeviceCheck[] = [];
  if (typeof navigator !== "undefined" && navigator.onLine === false) {
    return [{ id: "network.online", group: "network", label: "Connection", status: "warn", evidence: "reported", detail: "This device is offline.", guidance: "Only offline titles can play until the connection returns." }];
  }
  let probeUrl = SPEED_TEST_PATH;
  const samples: number[] = [];
  try {
    for (let i = 0; i < 5; i++) {
      const r = await timed(probeUrl, { Range: "bytes=0-0" });
      if (r.status === 404 && probeUrl === SPEED_TEST_PATH) {
        probeUrl = "/api/v1/system";
        i--;
        continue;
      }
      if (i > 0) samples.push(r.ms);
    }
    const latency = median(samples);
    const advice = latencyAdvice(latency);
    out.push({ id: "network.latency", group: "network", label: "Latency", status: advice.status, evidence: "measured", detail: `${Math.round(latency)} ms median round trip.`, guidance: advice.guidance });
  } catch {
    return [{ id: "network.latency", group: "network", label: "Latency", status: "fail", evidence: "measured", detail: "The server could not be reached.", guidance: "Check the connection or server status." }];
  }
  if (probeUrl !== SPEED_TEST_PATH) {
    out.push({ id: "network.throughput", group: "network", label: "Throughput", status: "info", evidence: "measured", detail: "This server does not offer a speed test yet." });
  } else {
    try {
      const r = await timed(SPEED_TEST_PATH, { Range: `bytes=0-${4 * 1024 * 1024 - 1}` });
      const rate = mbps(r.bytes, r.ms);
      const advice = throughputAdvice(rate);
      out.push({ id: "network.throughput", group: "network", label: "Throughput", status: advice.status, evidence: "measured", detail: `${rate.toFixed(1)} Mbps from a ${(r.bytes / 1024 / 1024).toFixed(0)} MB range request.`, guidance: advice.guidance });
    } catch {
      out.push({ id: "network.throughput", group: "network", label: "Throughput", status: "fail", evidence: "measured", detail: "The speed test did not complete.", guidance: "The connection dropped during the test." });
    }
  }
  const conn = (navigator as Navigator & { connection?: { effectiveType?: string; downlink?: number; saveData?: boolean } }).connection;
  if (conn?.effectiveType) {
    out.push({ id: "network.type", group: "network", label: "Connection type", status: conn.saveData ? "warn" : "info", evidence: "reported", detail: `${conn.effectiveType}${conn.downlink ? `, about ${conn.downlink} Mbps reported` : ""}${conn.saveData ? ", data saver on" : ""}.`, guidance: conn.saveData ? "Data saver is on; lower qualities are recommended." : undefined });
  }
  return out;
}

/** runDeviceTest performs every check; each group fails independently. */
export async function runDeviceTest(userId: string | undefined, onGroup?: (checks: DeviceCheck[]) => void): Promise<DeviceReport> {
  const checks: DeviceCheck[] = [];
  const add = (next: DeviceCheck[]) => {
    checks.push(...next);
    onGroup?.([...checks]);
  };
  const el = document.createElement("video");
  const steps: [CheckGroup, () => Promise<DeviceCheck[]> | DeviceCheck[]][] = [
    ["video", () => codecChecks(el)],
    ["streaming", streamingChecks],
    ["display", displayChecks],
    ["playback", async () => [await drmFreePlayback(), subtitleCheck()]],
    ["storage", storageChecks],
    ["offline", () => offlineChecks(userId)],
    ["network", networkChecks],
  ];
  for (const [group, step] of steps) {
    try {
      add(await step());
    } catch (error) {
      add([{ id: `${group}.error`, group, label: GROUP_LABELS[group], status: "warn", evidence: "reported", detail: `This check could not run: ${error instanceof Error ? error.message : "unknown error"}.` }]);
    }
  }
  const report: DeviceReport = { ranAt: new Date().toISOString(), userAgent: navigator.userAgent, checks };
  saveReport(report);
  return report;
}
